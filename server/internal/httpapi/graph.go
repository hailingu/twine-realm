// 本文件发布独立 v2 服务契约和依赖图入口，旧 v1 投影继续保持原形状。
package httpapi

import (
	"net/http"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// graph 从同一个持久快照恢复当前图，不把声明连线当作运行观察。
func (api *API) graph(w http.ResponseWriter, r *http.Request) {
	value, err := api.service.Graph(r.Context(), r.PathValue("id"))
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusOK, value)
}

// editGraph 严格解码有界判别操作，再由领域规则对最终图整体裁决。
func (api *API) editGraph(w http.ResponseWriter, r *http.Request) {
	var command project.GraphEditCommand
	if err := api.decode(w, r, "GraphEditCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	value, err := api.service.EditGraph(r.Context(), r.PathValue("id"), command)
	api.result(w, value, err, http.StatusOK)
}

// graphReceipt 仅核实原图请求，不让新回执污染已发布的旧种类。
func (api *API) graphReceipt(w http.ResponseWriter, r *http.Request) {
	if !project.ValidID(r.PathValue("id")) {
		api.failure(w, project.Failure("INVALID_REQUEST", "请求身份无效"))
		return
	}
	value, err := api.service.Receipt(r.Context(), r.PathValue("id"))
	if err == nil && (value.Kind != "editGraph" || value.Graph == nil) {
		api.failure(w, project.Failure("MUTATION_NOT_FOUND", "未找到对应图编辑回执"))
		return
	}
	api.result(w, value, err, http.StatusOK)
}
