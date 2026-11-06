// 本文件只受理已审阅部署，响应202不能作为迁移、运行或就绪完成的证据。
package httpapi

import (
	"net/http"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// deploymentRoutes 通过独立v4契约接入既有Host、Origin、JSON与身份边界。
func (api *API) deploymentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v4/deployment/capabilities", api.deploymentCapability)
	mux.HandleFunc("POST /api/v4/projects/{id}/services/{serviceId}/deployment/preview", api.deploymentPreview)
	mux.HandleFunc("POST /api/v4/projects/{id}/services/{serviceId}/deployments", api.deploymentAccept)
	mux.HandleFunc("GET /api/v4/projects/{id}/services/{serviceId}/deployment", api.deploymentSnapshot)
	mux.HandleFunc("GET /api/v4/projects/{id}/services/{serviceId}/deployments/{operationId}", api.deploymentOperation)
	mux.HandleFunc("GET /api/v4/deployment-mutations/{id}", api.deploymentReceipt)
}

// requireDeployment 未装配时明确拒绝，不以内存模拟部署或凭据。
func (api *API) requireDeployment(w http.ResponseWriter) *servicedeploy.Service {
	if api.deploy == nil {
		api.failure(w, project.Failure("DEPLOY_DISABLED", "业务部署未装配，构建与画布仍可使用"))
	}
	return api.deploy
}

// deploymentCapability 如实区分配置与运行结果。
func (api *API) deploymentCapability(w http.ResponseWriter, r *http.Request) {
	if service := api.requireDeployment(w); service != nil {
		api.write(w, http.StatusOK, service.Capability())
	}
}

// deploymentPreview 检查当前输入与环境，不接受外部写入或运行目标。
func (api *API) deploymentPreview(w http.ResponseWriter, r *http.Request) {
	service := api.requireDeployment(w)
	if service == nil {
		return
	}
	var command servicedeploy.PreviewRequest
	if err := api.decode(w, r, "DeployPreviewRequest", &command); err != nil {
		api.failure(w, err)
		return
	}
	value, err := service.Preview(r.Context(), r.PathValue("id"), r.PathValue("serviceId"), command)
	api.buildResult(w, http.StatusOK, value, err)
}

// deploymentAccept 原键与预览摘要必须共同保持，后续执行由持久工作器拥有。
func (api *API) deploymentAccept(w http.ResponseWriter, r *http.Request) {
	service := api.requireDeployment(w)
	if service == nil {
		return
	}
	var command servicedeploy.Command
	if err := api.decode(w, r, "DeployCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	value, err := service.Accept(r.Context(), r.PathValue("id"), r.PathValue("serviceId"), command)
	api.buildResult(w, http.StatusAccepted, value, err)
}

// deploymentSnapshot 只读一致元数据与最后采集时间。
func (api *API) deploymentSnapshot(w http.ResponseWriter, r *http.Request) {
	service := api.requireDeployment(w)
	if service == nil {
		return
	}
	value, err := service.Snapshot(r.Context(), r.PathValue("id"), r.PathValue("serviceId"))
	api.buildResult(w, http.StatusOK, value, err)
}

// deploymentOperation 保留原目标的有限诊断，不公开运行原日志。
func (api *API) deploymentOperation(w http.ResponseWriter, r *http.Request) {
	service := api.requireDeployment(w)
	if service == nil {
		return
	}
	value, err := service.Operation(r.Context(), r.PathValue("id"), r.PathValue("serviceId"), r.PathValue("operationId"))
	api.buildResult(w, http.StatusOK, value, err)
}

// deploymentReceipt 在禁用执行器时也能核实原键，不重新运行副作用。
func (api *API) deploymentReceipt(w http.ResponseWriter, r *http.Request) {
	service := api.requireDeployment(w)
	if service == nil {
		return
	}
	value, err := service.Receipt(r.Context(), r.PathValue("id"))
	api.buildResult(w, http.StatusOK, value, err)
}
