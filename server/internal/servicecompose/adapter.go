// Package servicecompose 将已接受的Go候选运行到原MySQL私网，不提供任意Docker执行入口。
package servicecompose

import (
	"context"
	"fmt"
	"os"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// Sources 提供已接受物理计划与原数据库资源，不把管理秘密存入领域对象。
type Sources interface {
	RuntimeSnapshot(context.Context, string) (mysqlruntime.Snapshot, error)
	RuntimeDeployment(context.Context, string) (*servicedeploy.Target, error)
}

// Driver 复用原运行根、项目锁和构建镜像验证，各自句柄仍由平台生命周期关闭。
type Driver struct {
	sources Sources
	runtime *mysqlruntime.Driver
	build   *servicebuild.Driver
}

// NewDriver 必须先于工作器装配，以便原MySQL启动也核实同一Compose扩展。
func NewDriver(sources Sources, runtime *mysqlruntime.Driver, build *servicebuild.Driver) (*Driver, error) {
	if sources == nil || runtime == nil || build == nil {
		return nil, fmt.Errorf("业务部署必须显式启用原运行与构建适配器")
	}
	value := &Driver{sources, runtime, build}
	if err := runtime.SetComposeExtensions(value); err != nil {
		return nil, err
	}
	return value, nil
}

// Prepare 只读检查真实数据库与镜像，不生成业务秘密或建立外部资源。
func (driver *Driver) Prepare(ctx context.Context, runtime mysqlruntime.Snapshot, build servicebuild.Snapshot) (servicedeploy.Database, servicedeploy.Environment, error) {
	var database servicedeploy.Database
	var environment servicedeploy.Environment
	if runtime.Target == nil || runtime.Operation == nil || runtime.Target.Desired != "running" || runtime.Operation.State != "succeeded" || runtime.Observation == nil || !runtime.Observation.Ready || runtime.Freshness != "fresh" {
		return database, environment, servicedeploy.Failure("DEPLOY_DATABASE_NOT_READY", "先将原MySQL启动至近期认证就绪，再审阅业务部署", false)
	}
	if build.Candidate == nil || build.Target == nil {
		return database, environment, servicedeploy.Failure("DEPLOY_CANDIDATE_REQUIRED", "先完成当前固定源码的真实检查与镜像构建", false)
	}
	if err := driver.build.VerifyCandidate(ctx, *build.Candidate); err != nil {
		return database, environment, err
	}
	resource := runtime.Target.Resource
	info, err := driver.runtime.BindingDatabase(ctx, resource)
	if err != nil {
		return database, environment, err
	}
	if info.ServerUUID != runtime.Observation.ServerUUID {
		return database, environment, servicedeploy.Failure("DEPLOY_DATABASE_CHANGED", "MySQL实际UUID与原目标不同", false)
	}
	digest, err := mysqlruntime.Fingerprint(resource)
	if err != nil {
		return database, environment, err
	}
	database = servicedeploy.Database{ResourceID: resource.ID, InstallationID: resource.InstallationID, DaemonID: resource.DaemonID, ComponentID: resource.ComponentID, Generation: runtime.Generation, ResourceDigest: digest, ConfigurationHash: resource.ConfigurationHash, ServerUUID: info.ServerUUID, Network: resource.Network, NetworkCIDR: info.NetworkCIDR, Volume: resource.Volume, Container: resource.Container, Image: resource.Image}
	buildEnv, runtimeEnv := driver.build.Environment(), driver.runtime.Environment()
	environment = servicedeploy.Environment{Context: runtimeEnv.Context, DaemonID: runtimeEnv.DaemonID, DockerVersion: buildEnv.DockerVersion, ComposeVersion: runtimeEnv.ComposeVersion, Architecture: buildEnv.Architecture, BindSourceMode: runtimeEnv.BindSourceMode, UID: os.Getuid(), GID: os.Getgid(), BuildInstallationID: driver.build.InstallationID(), RuntimeInstallationID: driver.runtime.InstallationID()}
	return database, environment, nil
}

// bindingRequest 核实原资源内容摘要和身份，不将后来画布草稿作为执行输入。
func (driver *Driver) bindingRequest(ctx context.Context, plan servicedeploy.Plan) (mysqlruntime.BindingRequest, error) {
	if err := servicedeploy.ValidatePlan(plan); err != nil {
		return mysqlruntime.BindingRequest{}, err
	}
	if err := driver.verifyPlanEnvironment(plan); err != nil {
		return mysqlruntime.BindingRequest{}, err
	}
	value, err := driver.sources.RuntimeSnapshot(ctx, plan.ProjectID)
	if err != nil {
		return mysqlruntime.BindingRequest{}, err
	}
	if value.Target == nil {
		return mysqlruntime.BindingRequest{}, servicedeploy.Failure("DEPLOY_DATABASE_MISSING", "原MySQL目标已缺失", true)
	}
	resource := value.Target.Resource
	digest, err := mysqlruntime.Fingerprint(resource)
	if err != nil {
		return mysqlruntime.BindingRequest{}, err
	}
	if digest != plan.Database.ResourceDigest || resource.ID != plan.Database.ResourceID || resource.InstallationID != plan.Environment.RuntimeInstallationID || resource.DaemonID != plan.Environment.DaemonID || plan.Environment.BuildInstallationID != driver.build.InstallationID() {
		return mysqlruntime.BindingRequest{}, servicedeploy.Failure("DEPLOY_DATABASE_CHANGED", "原MySQL资源或部署安装已改变", true)
	}
	return mysqlruntime.BindingRequest{Resource: resource, ServerUUID: plan.Database.ServerUUID, ServiceID: plan.ServiceID, ApplicationUser: plan.Resources.ApplicationUser, MigrationUser: plan.Resources.MigrationUser, AccountHost: plan.Resources.AccountHost, Binding: plan.Binding}, nil
}

// verifyPlanEnvironment 将持久环境与启动时受信安装逐项核对，避免JSON损坏改变context或宿主用户。
func (driver *Driver) verifyPlanEnvironment(plan servicedeploy.Plan) error {
	b, r := driver.build.Environment(), driver.runtime.Environment()
	expected := servicedeploy.Environment{Context: r.Context, DaemonID: r.DaemonID, DockerVersion: b.DockerVersion, ComposeVersion: r.ComposeVersion, Architecture: b.Architecture, BindSourceMode: r.BindSourceMode, UID: os.Getuid(), GID: os.Getgid(), BuildInstallationID: driver.build.InstallationID(), RuntimeInstallationID: driver.runtime.InstallationID()}
	if plan.Environment != expected {
		return servicedeploy.Failure("DEPLOY_ENVIRONMENT_CONFLICT", "持久部署环境与当前原安装不一致，禁止改变context或用途用户", false)
	}
	return nil
}

// RuntimeOverlay 由同库已接受目标确定，原MySQL重启不能忽略已部署业务容器。
func (driver *Driver) RuntimeOverlay(ctx context.Context, resource mysqlruntime.Resource) (*mysqlruntime.ComposeOverlay, error) {
	target, err := driver.sources.RuntimeDeployment(ctx, resource.ProjectID)
	if err != nil || target == nil {
		return nil, err
	}
	request, err := driver.bindingRequest(ctx, target.Plan)
	if err != nil {
		return nil, err
	}
	if request.Resource.ID != resource.ID {
		return nil, servicedeploy.Failure("DEPLOY_DATABASE_CHANGED", "Compose扩展不属于原MySQL资源", true)
	}
	files, err := driver.runtime.BindingPaths(request)
	if err != nil {
		return nil, err
	}
	data, err := composeMaterial(target.Plan, files)
	return &mysqlruntime.ComposeOverlay{ServiceID: target.ServiceID, Data: data}, err
}
