// 本文件发布源码版本的增量入口，原有 v1 项目和回执形状保持兼容。
package httpapi

import (
	"net/http"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// source 观察失败仍保留已保存版本，只有关联/存储错误才成为 HTTP 错误。
func (api *API) source(w http.ResponseWriter, r *http.Request) {
	value, err := api.service.Source(r.Context(), r.PathValue("id"), r.PathValue("serviceId"))
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusOK, value)
}

// pinSource 校验新命令后固定完整 commit，不改写任何源码工作区。
func (api *API) pinSource(w http.ResponseWriter, r *http.Request) {
	var command project.PinSourceCommand
	if err := api.decode(w, r, "PinSourceCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	value, err := api.service.PinSource(r.Context(), r.PathValue("id"), r.PathValue("serviceId"), command)
	api.result(w, value, err, http.StatusOK)
}

// sourceReceipt 核实源码原请求；不让新回执类型改变原有 Receipt 契约。
func (api *API) sourceReceipt(w http.ResponseWriter, r *http.Request) {
	if !project.ValidID(r.PathValue("id")) {
		api.failure(w, project.Failure("INVALID_REQUEST", "请求身份无效"))
		return
	}
	value, err := api.service.Receipt(r.Context(), r.PathValue("id"))
	if err == nil && (value.Kind != "pinSource" || value.SourceVersion == nil) {
		api.failure(w, project.Failure("MUTATION_NOT_FOUND", "未找到对应源码保存回执"))
		return
	}
	api.result(w, value, err, http.StatusOK)
}
