// 本文件只受理持久目标并提供恢复查询，202不等于MySQL已经运行成功。
package httpapi

import (
	"net/http"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// runtimeRoutes 运行v1与旧画布、源码API分开，不更改原响应形状。
func (api *API) runtimeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/runtime/capabilities", api.runtimeCapabilities)
	mux.HandleFunc("GET /api/v1/projects/{id}/runtime", api.runtimeSnapshot)
	mux.HandleFunc("POST /api/v1/projects/{id}/runtime/{componentId}/targets", api.runtimeAccept)
	mux.HandleFunc("GET /api/v1/projects/{id}/runtime/operations/{operationId}", api.runtimeOperation)
	mux.HandleFunc("GET /api/v1/projects/{id}/runtime/events", api.runtimeEvents)
	mux.HandleFunc("GET /api/v1/runtime-mutations/{id}", api.runtimeReceipt)
}

// requireRuntime 在未装配能力时明确拒绝，不回退到临时内存或客户端推测。
func (api *API) requireRuntime(w http.ResponseWriter) *mysqlruntime.Service {
	if api.runtime == nil {
		api.failure(w, project.Failure("RUNTIME_DISABLED", "本机运行服务未装配，项目和配置草稿仍可使用"))
	}
	return api.runtime
}

// runtimeCapabilities 如实报告显式配置，不能据此推断Docker此刻可用。
func (api *API) runtimeCapabilities(w http.ResponseWriter, r *http.Request) {
	service := api.requireRuntime(w)
	if service == nil {
		return
	}
	api.write(w, http.StatusOK, service.Capability())
}

// runtimeSnapshot 包含独立目标与操作版本，以及最后事实的时间和新鲜度。
func (api *API) runtimeSnapshot(w http.ResponseWriter, r *http.Request) {
	service := api.requireRuntime(w)
	if service == nil {
		return
	}
	value, err := service.Snapshot(r.Context(), r.PathValue("id"))
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusOK, value)
}

// runtimeAccept 契约校验与同库受理完成后返回202，不等待外部Docker动作。
func (api *API) runtimeAccept(w http.ResponseWriter, r *http.Request) {
	service := api.requireRuntime(w)
	if service == nil {
		return
	}
	var command mysqlruntime.Command
	if err := api.decode(w, r, "RuntimeCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	value, err := service.Accept(r.Context(), r.PathValue("id"), r.PathValue("componentId"), command)
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusAccepted, value)
}

// runtimeOperation 查询原操作与步骤，即使目标后来被新请求替代。
func (api *API) runtimeOperation(w http.ResponseWriter, r *http.Request) {
	service := api.requireRuntime(w)
	if service == nil {
		return
	}
	value, err := service.Operation(r.Context(), r.PathValue("id"), r.PathValue("operationId"))
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusOK, value)
}

// runtimeEvents 保留MySQL领域审计页，不把它的游标混成v5全项目SSE序号。
func (api *API) runtimeEvents(w http.ResponseWriter, r *http.Request) {
	service := api.requireRuntime(w)
	if service == nil {
		return
	}
	after := r.URL.Query().Get("after")
	if after == "" {
		after = "0"
	}
	value, err := service.Events(r.Context(), r.PathValue("id"), after)
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusOK, value)
}

// runtimeReceipt 核实响应丢失的原键，不接受通过换键复制副作用。
func (api *API) runtimeReceipt(w http.ResponseWriter, r *http.Request) {
	service := api.requireRuntime(w)
	if service == nil {
		return
	}
	value, err := service.Receipt(r.Context(), r.PathValue("id"))
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusOK, value)
}
