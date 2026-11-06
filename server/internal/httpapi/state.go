// 本文件提供版本化一致读取，状态事件不能成为用户编辑的第二确认入口。
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectstate"
)

// stateRoutes 保留旧 API 形状，聚合读取与通知仅由 v5 暴露。
func (api *API) stateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v5/projects/{id}/state", api.projectState)
	mux.HandleFunc("GET /api/v5/projects/{id}/state/events", api.stateStream)
}

// requireState 没有装配时显式拒绝，不拼接各模块独立快照冒充一致读取。
func (api *API) requireState(w http.ResponseWriter) *projectstate.Service {
	if api.state == nil {
		api.failure(w, project.Failure("STATE_DISABLED", "项目一致状态读取未装配，请保留当前草稿"))
	}
	return api.state
}

// projectState 完整编码并核实上限后才写成功，不发送截断的部分信封。
func (api *API) projectState(w http.ResponseWriter, r *http.Request) {
	service := api.requireState(w)
	if service == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	value, err := service.Snapshot(ctx, r.PathValue("id"))
	if err != nil {
		api.failure(w, err)
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		api.failure(w, fmt.Errorf("完整状态编码失败"))
		return
	}
	if len(data) > projectstate.DefaultPolicy().MaxSnapshotBytes {
		api.failure(w, project.Failure("STATE_SNAPSHOT_LIMIT", "完整状态超过当前版本读取上限，请保留草稿并调整项目规模"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		api.logger.Warn("项目状态响应写入失败")
	}
}

// statePage 为每次有界查询设置独立期限，等待客户端不持有数据库连接。
func (api *API) statePage(ctx context.Context, id, after string) (projectstate.Page, error) {
	reading, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return api.state.Events(reading, id, after)
}
