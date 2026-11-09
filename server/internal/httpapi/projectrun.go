// 本文件提供独立v6生命周期契约，旧状态与事件形状不变。
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"github.com/hailingu/twine-realm/server/internal/projectstate"
	"net/http"
	"time"
)

// lifecycleReader 要求v6完整读取使用单个数据库快照。
type lifecycleReader interface {
	ProjectStateV6(context.Context, string) (projectstate.SnapshotV6, error)
}

// lifecycleRoutes 不通过SSE受理命令。
func (api *API) lifecycleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v6/projects/{id}/lifecycle/receipts/{mutationId}/retry", api.lifecycleRetry)
	mux.HandleFunc("GET /api/v6/projects/{id}/state", api.lifecycleState)
	mux.HandleFunc("GET /api/v6/projects/{id}/lifecycle", api.lifecycleRead)
	mux.HandleFunc("POST /api/v6/projects/{id}/lifecycle", api.lifecycleAccept)
	mux.HandleFunc("GET /api/v6/projects/{id}/lifecycle/receipts/{mutationId}", api.lifecycleReceipt)
}

// requireLifecycle 对未装配执行能力显式返回失败。
func (api *API) requireLifecycle(w http.ResponseWriter) bool {
	if api.run == nil {
		api.failure(w, project.Failure("PROJECT_RUN_DISABLED", "未装配项目生命周期"))
		return false
	}
	return true
}

// lifecycleState 在同一RR事务返回完整事实。
func (api *API) lifecycleState(w http.ResponseWriter, r *http.Request) {
	if api.runState == nil {
		api.failure(w, project.Failure("STATE_DISABLED", "未装配完整状态"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	value, err := api.runState.ProjectStateV6(ctx, r.PathValue("id"))
	if err != nil {
		api.failure(w, err)
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		api.failure(w, fmt.Errorf("完整生命周期状态编码失败"))
		return
	}
	if len(data) > projectstate.DefaultPolicy().MaxSnapshotBytes {
		api.failure(w, project.Failure("STATE_SNAPSHOT_LIMIT", "完整状态超过读取上限，请保留草稿"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		api.logger.Warn("生命周期状态响应写入失败")
	}

}

// lifecycleRead 返回当前持久父目标。
func (api *API) lifecycleRead(w http.ResponseWriter, r *http.Request) {
	if !api.requireLifecycle(w) {
		return
	}
	value, err := api.run.Snapshot(r.Context(), r.PathValue("id"))
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusOK, value)
}

// lifecycleAccept 受理成功只表示持久命令，不宣称外部停止完成。
func (api *API) lifecycleAccept(w http.ResponseWriter, r *http.Request) {
	if !api.requireLifecycle(w) {
		return
	}
	var command projectrun.Command
	if err := api.decode(w, r, "ProjectRunCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	value, err := api.run.Accept(r.Context(), r.PathValue("id"), command)
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusAccepted, value)
}

// lifecycleReceipt 用原请求键核实已接受结果，不重发外部动作。
func (api *API) lifecycleReceipt(w http.ResponseWriter, r *http.Request) {
	if !api.requireLifecycle(w) {
		return
	}
	value, err := api.run.Receipt(r.Context(), r.PathValue("id"), r.PathValue("mutationId"))
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusOK, value)
}

// lifecycleRetry 仅显式续跑同一个已知失败原操作。
func (api *API) lifecycleRetry(w http.ResponseWriter, r *http.Request) {
	if !api.requireLifecycle(w) {
		return
	}
	value, err := api.run.Retry(r.Context(), r.PathValue("id"), r.PathValue("mutationId"))
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusAccepted, value)
}
