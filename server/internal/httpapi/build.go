// 本文件只受理构建与取消并读取持久结果，202不表示检查或镜像已成功。
package httpapi

import (
	"net/http"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// buildRoutes 使用独立v3契约，不改变已发布的画布、源码或运行响应。
func (api *API) buildRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v3/build/capabilities", api.buildCapability)
	mux.HandleFunc("GET /api/v3/projects/{id}/services/{serviceId}/build", api.buildSnapshot)
	mux.HandleFunc("POST /api/v3/projects/{id}/services/{serviceId}/builds", api.buildAccept)
	mux.HandleFunc("GET /api/v3/projects/{id}/services/{serviceId}/builds/{operationId}", api.buildOperation)
	mux.HandleFunc("POST /api/v3/projects/{id}/services/{serviceId}/builds/{operationId}/cancel", api.buildCancel)
	mux.HandleFunc("GET /api/v3/build-mutations/{id}", api.buildReceipt)
}

// requireBuild 未装配时明确拒绝，不以内存模拟持久构建。
func (api *API) requireBuild(w http.ResponseWriter) *servicebuild.Service {
	if api.build == nil {
		api.failure(w, project.Failure("BUILD_DISABLED", "构建服务未装配，源码和画布仍可使用"))
	}
	return api.build
}

// buildCapability 如实报告配置，不推断Docker健康或业务检查结果。
func (api *API) buildCapability(w http.ResponseWriter, r *http.Request) {
	service := api.requireBuild(w)
	if service != nil {
		api.write(w, http.StatusOK, service.Capability())
	}
}

// buildSnapshot 将历史候选与当前输入可用性分别返回。
func (api *API) buildSnapshot(w http.ResponseWriter, r *http.Request) {
	service := api.requireBuild(w)
	if service == nil {
		return
	}
	value, err := service.Snapshot(r.Context(), r.PathValue("id"), r.PathValue("serviceId"))
	api.buildResult(w, http.StatusOK, value, err)
}

// buildAccept 只接受已保存的完整commit和明确工程方案，不接受任意命令。
func (api *API) buildAccept(w http.ResponseWriter, r *http.Request) {
	service := api.requireBuild(w)
	if service == nil {
		return
	}
	var command servicebuild.Command
	if err := api.decode(w, r, "BuildCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	value, err := service.Accept(r.Context(), r.PathValue("id"), r.PathValue("serviceId"), command)
	api.buildResult(w, http.StatusAccepted, value, err)
}

// buildOperation 返回原操作，即使当前源码后来改变仍保留追溯。
func (api *API) buildOperation(w http.ResponseWriter, r *http.Request) {
	service := api.requireBuild(w)
	if service == nil {
		return
	}
	value, err := service.Operation(r.Context(), r.PathValue("id"), r.PathValue("serviceId"), r.PathValue("operationId"))
	api.buildResult(w, http.StatusOK, value, err)
}

// buildCancel 持久接受明确取消，最终沙箱收回由后台拥有者核实。
func (api *API) buildCancel(w http.ResponseWriter, r *http.Request) {
	service := api.requireBuild(w)
	if service == nil {
		return
	}
	var command servicebuild.CancelCommand
	if err := api.decode(w, r, "BuildCancelCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	value, err := service.Cancel(r.Context(), r.PathValue("id"), r.PathValue("serviceId"), r.PathValue("operationId"), command)
	api.buildResult(w, http.StatusAccepted, value, err)
}

// buildReceipt 不生成新操作，网络未知时必须保持原键核实。
func (api *API) buildReceipt(w http.ResponseWriter, r *http.Request) {
	service := api.requireBuild(w)
	if service == nil {
		return
	}
	value, err := service.Receipt(r.Context(), r.PathValue("id"))
	api.buildResult(w, http.StatusOK, value, err)
}

// buildResult 统一传播构建领域错误，只有实际同库结果才能作为受理响应。
func (api *API) buildResult(w http.ResponseWriter, status int, value any, err error) {
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, status, value)
}
