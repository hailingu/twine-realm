// 本文件发布受控目录、完整画布与组件草稿编辑，不开放运行或任意 schema 注册。
package httpapi

import (
	"net/http"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// catalog 返回本次平台实际内置的版本，而不是宣称所有中间件均已支持。
func (api *API) catalog(w http.ResponseWriter, r *http.Request) {
	api.write(w, http.StatusOK, api.service.Catalog())
}

// canvas 读取同库一致的服务和标准组件投影。
func (api *API) canvas(w http.ResponseWriter, r *http.Request) {
	value, err := api.service.Canvas(r.Context(), r.PathValue("id"))
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusOK, value)
}

// addMiddleware 将用户显式确认的合法草稿加入画布，不创建容器或数据库。
func (api *API) addMiddleware(w http.ResponseWriter, r *http.Request) {
	api.putMiddleware(w, r, "", http.StatusCreated)
}

// updateMiddleware 替换实例的完整配置候选，定义升级不属于此入口。
func (api *API) updateMiddleware(w http.ResponseWriter, r *http.Request) {
	api.putMiddleware(w, r, r.PathValue("componentId"), http.StatusOK)
}

// putMiddleware 共用严格解码和领域入口，服务端目录是最终裁决。
func (api *API) putMiddleware(w http.ResponseWriter, r *http.Request, componentID string, status int) {
	var command project.MiddlewareCommand
	if err := api.decode(w, r, "MiddlewareCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	value, err := api.service.PutMiddleware(r.Context(), r.PathValue("id"), componentID, command)
	api.result(w, value, err, status)
}

// canvasLayout 一次保存所有类别节点位置，旧服务布局端点继续保持原契约。
func (api *API) canvasLayout(w http.ResponseWriter, r *http.Request) {
	var command project.CanvasLayoutCommand
	if err := api.decode(w, r, "CanvasLayoutCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	value, err := api.service.SaveCanvasLayout(r.Context(), r.PathValue("id"), command)
	api.result(w, value, err, http.StatusOK)
}

// canvasReceipt 核实原画布请求，未知结果不能改键复制节点。
func (api *API) canvasReceipt(w http.ResponseWriter, r *http.Request) {
	if !project.ValidID(r.PathValue("id")) {
		api.failure(w, project.Failure("INVALID_REQUEST", "请求身份无效"))
		return
	}
	value, err := api.service.Receipt(r.Context(), r.PathValue("id"))
	if err == nil && value.Canvas == nil {
		api.failure(w, project.Failure("MUTATION_NOT_FOUND", "未找到对应画布回执"))
		return
	}
	api.result(w, value, err, http.StatusOK)
}
