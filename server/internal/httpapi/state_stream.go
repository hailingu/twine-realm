// 本文件以请求拥有的同步循环发送有界 SSE，流断开不取消任何后台持久操作。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectstate"
)

// stateCursor 同时提供 header/query 时必须一致，重复或未知参数不能被静默忽略。
func stateCursor(r *http.Request) (string, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", project.Failure("INVALID_REQUEST", "状态流参数转义无效")
	}
	if len(query) > 1 || len(query["after"]) > 1 || len(r.Header.Values("Last-Event-ID")) > 1 {
		return "", project.Failure("INVALID_REQUEST", "事件游标只接受一个规范值")
	}
	for key := range query {
		if key != "after" {
			return "", project.Failure("INVALID_REQUEST", "状态流包含不支持的参数")
		}
	}
	header, value := r.Header.Get("Last-Event-ID"), query.Get("after")
	if header != "" && value != "" && header != value {
		return "", project.Failure("INVALID_REQUEST", "事件恢复游标不一致")
	}
	if header != "" {
		value = header
	}
	_, err = projectstate.Cursor(value)
	return value, err
}

// stateStream 在写头前核实游标和限流，写头后只发送契约控制帧或安全结束。
func (api *API) stateStream(w http.ResponseWriter, r *http.Request) {
	if api.requireState(w) == nil {
		return
	}
	id := r.PathValue("id")
	if !project.ValidID(id) {
		api.failure(w, project.Failure("INVALID_REQUEST", "项目身份无效"))
		return
	}
	after, err := stateCursor(r)
	if err != nil {
		api.failure(w, err)
		return
	}
	if !api.streams.acquire(id) {
		w.Header().Set("Retry-After", strconv.Itoa(projectstate.DefaultPolicy().RetrySeconds))
		api.write(w, http.StatusTooManyRequests, struct {
			Error *project.Error `json:"error"`
		}{project.Failure("STATE_STREAM_LIMIT", "状态连接已达上限，请按退避期限重试")})
		return
	}
	defer api.streams.release(id)
	page, err := api.statePage(r.Context(), id, after)
	if err != nil {
		api.failure(w, err)
		return
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		api.failure(w, fmt.Errorf("当前HTTP运输不支持有界状态流"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	next, err := api.drainStatePages(r.Context(), w, id, page)
	if err != nil {
		api.stateStreamFailure(w, id, err)
		return
	}
	api.continueStateStream(r.Context(), w, id, next)
}

// continueStateStream 所有计时器和查询归当前请求，退出前同步回收。
func (api *API) continueStateStream(ctx context.Context, w http.ResponseWriter, id, after string) {
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	heartbeat := time.NewTicker(time.Duration(projectstate.DefaultPolicy().HeartbeatSeconds) * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if err := writeStateBytes(w, []byte(": heartbeat\n\n")); err != nil {
				api.logger.Warn("状态心跳写入结束")
				return
			}
		case <-poll.C:
			page, err := api.statePage(ctx, id, after)
			if err != nil {
				api.stateStreamFailure(w, id, err)
				return
			}
			if len(page.Events) == 0 {
				continue
			}
			next, err := api.drainStatePages(ctx, w, id, page)
			if err != nil {
				api.stateStreamFailure(w, id, err)
				return
			}
			after = next
		}
	}
}

// drainStatePages 逐页立即追平，不积压全历史，也不人为每页等待一秒。
func (api *API) drainStatePages(ctx context.Context, w http.ResponseWriter, id string, page projectstate.Page) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return page.Next, err
		}
		if err := api.sendStatePage(w, id, page); err != nil {
			return page.Next, err
		}
		if !page.HasMore {
			return page.Next, nil
		}
		next, err := api.statePage(ctx, id, page.Next)
		if err != nil {
			return page.Next, err
		}
		page = next
	}
}

// sendStatePage 只在一页已经追平时发送独立控制帧，不把头游标塞进业务 id。
func (api *API) sendStatePage(w http.ResponseWriter, id string, page projectstate.Page) error {
	for _, event := range page.Events {
		if err := writeStateFrame(w, "state", event.Sequence, event); err != nil {
			return err
		}
	}
	if !page.HasMore {
		return writeStateFrame(w, "caught_up", "", projectstate.CaughtUp{SchemaVersion: 1, ProjectID: id, Cursor: page.Next})
	}
	return nil
}

// stateStreamFailure 窗口或连续性失效明确要求完整恢复，存储未知仅安全断流。
func (api *API) stateStreamFailure(w http.ResponseWriter, id string, err error) {
	var domain *project.Error
	if errors.As(err, &domain) && (domain.Code == "STATE_CURSOR_EXPIRED" || domain.Code == "STATE_CURSOR_AHEAD" || domain.Code == "STATE_EVENT_GAP") {
		if err := writeStateFrame(w, "resync", "", projectstate.Resync{SchemaVersion: 1, ProjectID: id, Code: domain.Code}); err != nil {
			api.logger.Warn("状态恢复控制写入结束")
		}
		return
	}
	api.logger.Warn("状态流读取结束", "code", "STORAGE_UNAVAILABLE")
}

// writeStateFrame JSON 保持单行，明确限制完整 UTF-8 传输帧大小。
func writeStateFrame(w http.ResponseWriter, kind, id string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	frame := "event: " + kind + "\n"
	if id != "" {
		frame += "id: " + id + "\n"
	}
	return writeStateBytes(w, []byte(frame+"data: "+string(data)+"\n\n"))
}

// writeStateBytes 每次写入有短期限，完成后清零等待心跳，避免延长已过期 deadline。
func writeStateBytes(w http.ResponseWriter, data []byte) error {
	if len(data) > projectstate.DefaultPolicy().MaxFrameBytes {
		return fmt.Errorf("状态帧超过固定上限")
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := controller.Flush(); err != nil {
		return err
	}
	return controller.SetWriteDeadline(time.Time{})
}
