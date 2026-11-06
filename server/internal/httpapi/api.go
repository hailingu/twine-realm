// Package httpapi 提供可组合的本机 HTTP 边界，不实现一期之外的账号系统。
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hailingu/twine-realm/server/contract"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// API 只依赖项目用例和已编译契约，不持有账号或会话状态。
type API struct {
	service *project.Service
	schemas map[string]*jsonschema.Schema
	logger  *slog.Logger
	runtime *mysqlruntime.Service
	build   *servicebuild.Service
}

// Features 显式装配独立能力，未启用的执行器不会隐式产生本机副作用。
type Features struct {
	Runtime *mysqlruntime.Service
	Build   *servicebuild.Service
}

// New 装配路由和最小本机防护，Host/Origin 来源必须由启动配置确定。
func New(service *project.Service, host string, origins []string, webDirectory string, logger *slog.Logger, features ...Features) (http.Handler, error) {
	if len(features) > 1 {
		return nil, errors.New("HTTP仅接受一组明确能力装配")
	}
	schemas, err := contract.Load()
	if err != nil {
		return nil, err
	}
	api := &API{service: service, schemas: schemas, logger: logger}
	if len(features) == 1 {
		api.runtime, api.build = features[0].Runtime, features[0].Build
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		api.write(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /api/v1/projects", api.list)
	mux.HandleFunc("POST /api/v1/projects", api.create)
	mux.HandleFunc("GET /api/v1/projects/{id}", api.get)
	mux.HandleFunc("POST /api/v1/projects/{id}/services", api.register)
	mux.HandleFunc("PUT /api/v1/projects/{id}/layout", api.layout)
	mux.HandleFunc("GET /api/v1/projects/{id}/services/{serviceId}/source", api.source)
	mux.HandleFunc("PUT /api/v1/projects/{id}/services/{serviceId}/source", api.pinSource)
	mux.HandleFunc("GET /api/v1/source-mutations/{id}", api.sourceReceipt)
	mux.HandleFunc("GET /api/v1/catalog", api.catalog)
	mux.HandleFunc("GET /api/v1/projects/{id}/canvas", api.canvas)
	mux.HandleFunc("POST /api/v1/projects/{id}/canvas/middlewares", api.addMiddleware)
	mux.HandleFunc("PUT /api/v1/projects/{id}/canvas/middlewares/{componentId}", api.updateMiddleware)
	mux.HandleFunc("PUT /api/v1/projects/{id}/canvas/layout", api.canvasLayout)
	mux.HandleFunc("GET /api/v1/canvas-mutations/{id}", api.canvasReceipt)
	mux.HandleFunc("GET /api/v2/projects/{id}/graph", api.graph)
	mux.HandleFunc("POST /api/v2/projects/{id}/graph/edits", api.editGraph)
	mux.HandleFunc("GET /api/v2/graph-mutations/{id}", api.graphReceipt)
	mux.HandleFunc("GET /api/v1/mutations/{id}", api.receipt)
	api.runtimeRoutes(mux)
	api.buildRoutes(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		api.failure(w, project.Failure("NOT_FOUND", "接口不存在"))
	})
	mux.Handle("/", staticFiles(webDirectory))
	return api.localBoundary(mux, host, origins), nil
}

// list 返回持久摘要而非浏览器最近访问记录。
func (api *API) list(w http.ResponseWriter, r *http.Request) {
	values, err := api.service.List(r.Context())
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusOK, struct {
		Projects []project.Summary `json:"projects"`
	}{values})
}

// get 读取完整已提交项目并校验路径身份。
func (api *API) get(w http.ResponseWriter, r *http.Request) {
	if !project.ValidID(r.PathValue("id")) {
		api.failure(w, project.Failure("INVALID_REQUEST", "项目身份无效"))
		return
	}
	value, err := api.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, http.StatusOK, value)
}

// create 把创建请求交给显式幂等用例。
func (api *API) create(w http.ResponseWriter, r *http.Request) {
	var command project.CreateCommand
	if err := api.decode(w, r, "CreateCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	receipt, err := api.service.Create(r.Context(), command)
	api.result(w, receipt, err, http.StatusCreated)
}

// register 校验机器契约后登记已存在的本机仓库。
func (api *API) register(w http.ResponseWriter, r *http.Request) {
	var command project.RegisterCommand
	if err := api.decode(w, r, "RegisterCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	receipt, err := api.service.Register(r.Context(), r.PathValue("id"), command)
	api.result(w, receipt, err, http.StatusCreated)
}

// layout 提交全部节点位置，版本冲突保持为显式错误。
func (api *API) layout(w http.ResponseWriter, r *http.Request) {
	var command project.LayoutCommand
	if err := api.decode(w, r, "LayoutCommand", &command); err != nil {
		api.failure(w, err)
		return
	}
	receipt, err := api.service.SaveLayout(r.Context(), r.PathValue("id"), command)
	api.result(w, receipt, err, http.StatusOK)
}

// receipt 核实响应丢失的请求，不重新写入任何内容。
func (api *API) receipt(w http.ResponseWriter, r *http.Request) {
	if !project.ValidID(r.PathValue("id")) {
		api.failure(w, project.Failure("INVALID_REQUEST", "请求身份无效"))
		return
	}
	value, err := api.service.Receipt(r.Context(), r.PathValue("id"))
	if err == nil && (value.Kind == "pinSource" || value.Canvas != nil || value.Graph != nil) {
		api.failure(w, project.Failure("UNSUPPORTED_RECEIPT", "此类型回执需通过对应的专用核实入口读取"))
		return
	}
	api.result(w, value, err, http.StatusOK)
}

// decode 限流请求体并在反序列化成领域命令前执行同一发布 schema。
func (api *API) decode(w http.ResponseWriter, r *http.Request, name string, target any) error {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128*1024))
	if err != nil {
		return project.Failure("INVALID_REQUEST", "请求体过大或不可读取")
	}
	var unknown any
	if err := json.Unmarshal(data, &unknown); err != nil {
		return project.Failure("INVALID_REQUEST", "请求必须为有效 JSON")
	}
	if err := api.schemas[name].Validate(unknown); err != nil {
		return project.Failure("INVALID_REQUEST", "请求不符合版本一契约，请检查必填字段与取值")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return project.Failure("INVALID_REQUEST", "请求字段类型无效")
	}
	return nil
}

// result 只在应用用例确认提交后返回成功回执。
func (api *API) result(w http.ResponseWriter, value project.Receipt, err error, status int) {
	if err != nil {
		api.failure(w, err)
		return
	}
	api.write(w, status, value)
}

// write 统一响应编码和安全头，网络失败仅记录不含业务数据的诊断。
func (api *API) write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		api.logger.Warn("响应写入失败")
	}
}

// failure 不把驱动、Git 命令或连接串细节回显到客户端或日志。
func (api *API) failure(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	failure := project.Failure("STORAGE_UNAVAILABLE", "本机数据服务暂不可用，草稿已保留，请稍后核实或重试")
	var domain *project.Error
	if errors.As(err, &domain) {
		failure = domain
		switch domain.Code {
		case "PROJECT_NOT_FOUND", "SERVICE_NOT_FOUND", "COMPONENT_NOT_FOUND", "BINDING_NOT_FOUND", "MUTATION_NOT_FOUND", "NOT_FOUND", "RUNTIME_OPERATION_NOT_FOUND", "BUILD_OPERATION_NOT_FOUND":
			status = http.StatusNotFound
		case "DUPLICATE_REPOSITORY", "DUPLICATE_BINDING", "CONTRACT_VERSION_CONFLICT", "REVISION_CONFLICT", "IDEMPOTENCY_CONFLICT", "DEFINITION_CONFLICT", "RUNTIME_GENERATION_CONFLICT", "RUNTIME_COMPONENT_CONFLICT", "RUNTIME_CONFIGURATION_LOCKED", "RUNTIME_RETRY_CONFLICT", "RUNTIME_BUSY", "RUNTIME_INSTALLATION_CONFLICT":
			status = http.StatusConflict
		case "RESULT_UNKNOWN":
			status = http.StatusServiceUnavailable
		case "BUILD_GENERATION_CONFLICT", "BUILD_INSTALLATION_CONFLICT", "BUILD_OPERATION_CONFLICT", "BUILD_VERSION_CONFLICT", "BUILD_BUSY", "BUILD_RETRY_CONFLICT", "BUILD_RECOVERY_REQUIRED", "BUILD_SOURCE_CHANGED":
			status = http.StatusConflict
		case "RUNTIME_DISABLED", "BUILD_DISABLED":
			status = http.StatusServiceUnavailable
		case "FORBIDDEN":
			status = http.StatusForbidden
		case "UNSUPPORTED_MEDIA_TYPE":
			status = http.StatusUnsupportedMediaType
		default:
			status = http.StatusBadRequest
		}
	}
	if status >= 500 {
		api.logger.Warn("本机请求未确认", "code", failure.Code)
	}
	api.write(w, status, struct {
		Error *project.Error `json:"error"`
	}{failure})
}

// localBoundary 防止 DNS 重绑定、跨站写入和隐式表单请求；未来身份层可在外部组合。
func (api *API) localBoundary(next http.Handler, host string, origins []string) http.Handler {
	allowed := map[string]bool{}
	for _, origin := range origins {
		allowed[origin] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		defer func() {
			if recover() != nil {
				api.logger.Error("本机请求内部异常")
				api.failure(w, project.Failure("RESULT_UNKNOWN", "请求异常，结果尚未确认，请核实原请求"))
			}
		}()
		if err := validateLocalRequest(r, host, allowed); err != nil {
			api.failure(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// validateLocalRequest 把外部来源和写入媒体约束保持为可独立检查的入口规则。
func validateLocalRequest(r *http.Request, host string, allowed map[string]bool) error {
	if r.Host != host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return project.Failure("FORBIDDEN", "请求来源不在本机允许范围内")
	}
	if r.Method == "GET" || r.Method == "HEAD" {
		return nil
	}
	if !allowed[r.Header.Get("Origin")] || r.Header.Get("X-Twine-Request") != "1" {
		return project.Failure("FORBIDDEN", "需要本机同源请求")
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return project.Failure("UNSUPPORTED_MEDIA_TYPE", "只接受 JSON 请求")
	}
	return nil
}

// staticFiles 只开放构建产物，API 和本机仓库路径不会进入静态根目录。
func staticFiles(directory string) http.Handler {
	files := http.FileServer(http.Dir(directory))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/" {
			http.ServeFile(w, r, filepath.Join(directory, "index.html"))
			return
		}
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		if _, err := os.Stat(filepath.Join(directory, filepath.Clean(r.URL.Path))); err != nil {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}
