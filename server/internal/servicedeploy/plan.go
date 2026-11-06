// 本文件在只读事实上生成确定预览；受理事务还须重新核对声明与候选身份。
package servicedeploy

import (
	"fmt"
	"net"
	"reflect"
	"strings"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// MakePlan 只接受当前已检查候选、精确连线与已认证MySQL身份，不运行迁移或创建容器。
func MakePlan(graph project.GraphSnapshot, build servicebuild.Snapshot, request PreviewRequest, database Database, environment Environment) (Plan, error) {
	value := Plan{SchemaVersion: 1, ProjectID: graph.Project.ID, ServiceID: build.ServiceID, Request: request, Database: database, Environment: environment, Policy: DefaultPolicy()}
	if err := ValidatePreview(value.ProjectID, value.ServiceID, request); err != nil {
		return value, err
	}
	if graph.Project.Revision != request.BaseRevision {
		return value, project.Failure("REVISION_CONFLICT", "画布已改变，请重新审阅部署预览")
	}
	if err := currentCandidate(graph.Project, build, request.CandidateID); err != nil {
		return value, err
	}
	value.Candidate = *build.Candidate
	contract, binding, name, err := resolveDeclaration(graph, build.ServiceID, request.BindingID, request.Configuration)
	if err != nil {
		return value, err
	}
	value.Contract, value.Binding, value.ServiceName = contract, binding, name
	if database.ComponentID != binding.Target.ComponentID || database.ConfigurationHash == "" || !project.ValidID(database.ServerUUID) || database.Image != mysqlruntime.Image || database.InstallationID != environment.RuntimeInstallationID || database.DaemonID != environment.DaemonID {
		return value, project.Failure("DEPLOY_DATABASE_CHANGED", "MySQL身份或连线与本机环境不一致")
	}
	value.Resources, err = resourceNames(value)
	if err != nil {
		return value, err
	}
	value.RuntimeDigest, err = runtimeDigest(value)
	if err != nil {
		return value, err
	}
	value.Digest, err = planDigest(value)
	return value, err
}

// ValidatePreview 对同键查询之前也执行结构边界，不接受任意配置或执行参数。
func ValidatePreview(projectID, serviceID string, value PreviewRequest) error {
	if !project.ValidID(projectID) || !project.ValidID(serviceID) || !project.ValidID(value.CandidateID) || !project.ValidID(value.BindingID) || !mysqlruntime.Number(value.BaseRevision) || !mysqlruntime.Number(value.BaseGeneration) || value.HostPort < 1024 || value.HostPort > 65535 || value.Configuration == nil {
		return project.Failure("INVALID_REQUEST", "部署身份、版本、端口或配置无效")
	}
	if len(value.Configuration) != 0 {
		return project.Failure("DEPLOY_CONFIGURATION_UNSUPPORTED", "首个运行组合尚未提供业务自定义配置映射，仅支持满足服务schema的空配置")
	}
	return nil
}

// currentCandidate 将原始成功操作、全部检查与现在仍固定的仓库关联一起验证。
func currentCandidate(graph project.Project, build servicebuild.Snapshot, candidateID string) error {
	if build.Candidate == nil || build.Target == nil || build.Operation == nil || build.Candidate.ID != candidateID || build.Operation.State != "succeeded" || build.ProjectID != graph.ID {
		return project.Failure("DEPLOY_CANDIDATE_REQUIRED", "先完成当前固定源码的真实检查与镜像构建")
	}
	if build.Target.ProjectID != graph.ID || build.Target.ServiceID != build.ServiceID || build.Candidate.Source.ProjectID != graph.ID || build.Candidate.Source.ServiceID != build.ServiceID || build.CurrentSource.ProjectID != graph.ID || build.CurrentSource.ServiceID != build.ServiceID {
		return project.Failure("DEPLOY_CANDIDATE_INVALID", "候选、目标与当前服务归属不一致")
	}
	lease := servicebuild.Lease{Target: *build.Target, Operation: *build.Operation}
	completion := servicebuild.Completion{State: "succeeded", Code: build.Operation.Code, Message: build.Operation.Message, Candidate: build.Candidate}
	if err := servicebuild.ValidateCompletion(lease, completion); err != nil {
		return project.Failure("DEPLOY_CANDIDATE_INVALID", "构建候选缺少完整、相互一致的检查证据")
	}
	source := build.CurrentSource
	if source.PinnedCommit == nil || *source.PinnedCommit != build.Candidate.Source.PinnedCommit || source.RepositoryID != build.Candidate.Source.RepositoryID || source.RepositoryPath != build.Candidate.Source.RepositoryPath {
		return project.Failure("DEPLOY_SOURCE_CHANGED", "当前固定源码与原候选不同，请先重新构建")
	}
	profile, err := engineering.Load()
	if err != nil {
		return err
	}
	if build.Candidate.Profile != profile.Identity() || build.Candidate.Environment.OperatingSystem != "linux" {
		return project.Failure("DEPLOY_PROFILE_UNSUPPORTED", "候选不属于当前受支持的Go工程组合")
	}
	return nil
}

// resolveDeclaration 拒绝缺失、附加或过期依赖，不凭端口显示位置判断兼容。
func resolveDeclaration(graph project.GraphSnapshot, serviceID, bindingID string, configuration map[string]any) (project.DefinitionRef, project.DatabaseBinding, string, error) {
	var reference project.DefinitionRef
	var binding project.DatabaseBinding
	name := ""
	for _, node := range graph.Project.Nodes {
		if node.ID == serviceID {
			name = node.DisplayName
		}
	}
	contract, err := deploymentContract(graph.Graph, serviceID, configuration)
	if err != nil {
		return reference, binding, name, err
	}
	reference = contract.Reference
	count := 0
	for _, item := range graph.Graph.Bindings {
		if item.Source.ComponentID == serviceID {
			count++
			binding = item
		}
	}
	if name == "" || count != 1 || binding.ID != bindingID || reference != binding.SourceDefinition || binding.Protocol != "mysql" || binding.Contract != "mysql-wire/10" {
		return reference, binding, name, project.Failure("DEPLOY_BINDING_UNSUPPORTED", "首个运行组合需要此服务的一条有效MySQL依赖")
	}
	if err := requiredDependencies(contract.Document, binding); err != nil {
		return reference, binding, name, err
	}
	return reference, binding, name, nil
}

// deploymentContract 单独核实唯一契约与服务自有配置，不把选择和端口基数混在一起。
func deploymentContract(graph project.GraphState, serviceID string, configuration map[string]any) (value project.ServiceContract, err error) {
	count := 0
	for _, item := range graph.ServiceContracts {
		if item.ServiceID == serviceID {
			value = item
			count++
		}
	}
	if count != 1 {
		return value, project.Failure("DEPLOY_CONTRACT_REQUIRED", "部署需要此服务唯一的已保存接口契约")
	}
	definitions, err := catalog.New()
	if err != nil {
		return value, err
	}
	if err := definitions.ValidateServiceConfiguration(value.Document, configuration); err != nil {
		return value, err
	}
	_, actual, err := definitions.ValidateServiceDocument(value.Document)
	if err != nil {
		return value, err
	}
	if actual != value.Reference {
		return value, project.Failure("DEPLOY_CONTRACT_CHANGED", "服务契约内容与固定引用不一致")
	}
	return value, nil
}

// requiredDependencies 首版不能带着未满足的其他必需消费端口启动服务。
func requiredDependencies(document project.ServiceContractDocument, binding project.DatabaseBinding) error {
	for _, port := range document.Ports {
		if port.Required && port.ID != binding.Source.PortID {
			return project.Failure("DEPLOY_DEPENDENCY_UNSUPPORTED", "此服务还有未满足的必需依赖，不能部署不完整组合")
		}
	}
	return nil
}

// resourceNames 由稳定身份生成受限物理名；数据库账号Host使用真实私网掩码而非通配符。
func resourceNames(value Plan) (Resources, error) {
	_, network, err := net.ParseCIDR(value.Database.NetworkCIDR)
	if err != nil || network.IP.To4() == nil || value.Environment.UID < 1 || value.Environment.GID < 1 || value.Environment.Architecture != value.Candidate.Environment.Architecture || value.Environment.DaemonID != value.Candidate.Environment.DaemonID || value.Environment.BuildInstallationID == "" {
		return Resources{}, project.Failure("DEPLOY_ENVIRONMENT_CONFLICT", "运行环境、私网或非root身份未核实")
	}
	hash, err := servicebuild.Fingerprint([]string{value.Database.InstallationID, value.ProjectID, value.ServiceID})
	if err != nil {
		return Resources{}, err
	}
	account, err := servicebuild.Fingerprint(value.Binding.Credential)
	if err != nil {
		return Resources{}, err
	}
	prefix := "tr-" + strings.TrimPrefix(hash, "sha256:")[:24]
	credential := strings.TrimPrefix(account, "sha256:")[:24]
	return Resources{ComposeProject: value.Database.Container, Application: prefix + "-service", Migration: prefix + "-migration", IngressNetwork: prefix + "-http", ApplicationUser: "tra_" + credential, MigrationUser: "trm_" + credential, AccountHost: network.IP.String() + "/" + net.IP(network.Mask).String()}, nil
}

// runtimeDigest 排除纯并发前置版本，保留真正影响运行和资源归属的全部内容。
func runtimeDigest(value Plan) (string, error) {
	value.Request.BaseGeneration, value.Request.BaseRevision = "", ""
	value.Database.Generation = ""
	value.RuntimeDigest, value.Digest = "", ""
	return servicebuild.Fingerprint(value)
}

// planDigest 覆盖用户审阅的精确版本，不允许换端口或候选后沿用旧确认。
func planDigest(value Plan) (string, error) {
	value.Digest = ""
	return servicebuild.Fingerprint(value)
}

// ValidatePlan 在持久计划执行前重新核实摘要、名称和固定策略，防止损坏JSON授权外部执行。
func ValidatePlan(value Plan) error {
	if err := ValidatePreview(value.ProjectID, value.ServiceID, value.Request); err != nil {
		return err
	}
	names, err := resourceNames(value)
	if err != nil {
		return err
	}
	runtime, err := runtimeDigest(value)
	if err != nil {
		return err
	}
	digest, err := planDigest(value)
	if err != nil {
		return err
	}
	if value.SchemaVersion != 1 || names != value.Resources || value.Policy != DefaultPolicy() || runtime != value.RuntimeDigest || digest != value.Digest || !reflect.DeepEqual(value.Candidate.ID, value.Request.CandidateID) {
		return fmt.Errorf("持久部署计划与固定身份或策略不一致")
	}
	return nil
}
