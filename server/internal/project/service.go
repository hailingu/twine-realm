// 本文件协调校验、只读仓库检查和持久回执，不依赖 HTTP 或数据库驱动。
package project

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Service 是项目应用用例入口，未来身份中间件可在此边界外组合。
type Service struct {
	reader    Reader
	writer    Writer
	receipts  ReceiptReader
	inspector Inspector
}

// NewService 注入实际读取、原子写入、恢复与仓库观测端口。
func NewService(reader Reader, writer Writer, receipts ReceiptReader, inspector Inspector) *Service {
	return &Service{reader: reader, writer: writer, receipts: receipts, inspector: inspector}
}

// List 返回持久项目摘要，失败不伪装成空列表。
func (service *Service) List(ctx context.Context) ([]Summary, error) { return service.reader.List(ctx) }

// Get 读取明确项目的完整已确认版本。
func (service *Service) Get(ctx context.Context, id string) (Project, error) {
	return service.reader.Get(ctx, id)
}

// Receipt 查询原请求的持久结果，不重新执行副作用。
func (service *Service) Receipt(ctx context.Context, id string) (Receipt, error) {
	receipt, _, err := service.receipts.Receipt(ctx, id)
	receipt.Replayed = true
	return receipt, err
}

// Create 在规范化名称后创建项目，相同请求键只结清一次结果。
func (service *Service) Create(ctx context.Context, command CreateCommand) (Receipt, error) {
	command.Name = strings.TrimSpace(command.Name)
	if err := validName(command.Name); err != nil {
		return Receipt{}, err
	}
	if !ValidID(command.MutationID) {
		return Receipt{}, Failure("INVALID_REQUEST", "请求身份无效")
	}
	hash, err := requestHash("createProject", "", command)
	if err != nil {
		return Receipt{}, err
	}
	if receipt, found, err := service.replay(ctx, command.MutationID, hash); found || err != nil {
		return receipt, err
	}
	return service.writer.Create(ctx, command, hash)
}

// Register 先查原回执，再只读检查仓库，避免重放依赖已经变化的源目录。
func (service *Service) Register(ctx context.Context, id string, command RegisterCommand) (Receipt, error) {
	command.RepositoryPath = strings.TrimSpace(command.RepositoryPath)
	command.DisplayName = strings.TrimSpace(command.DisplayName)
	if err := validMutation(id, command.MutationID, command.BaseRevision); err != nil {
		return Receipt{}, err
	}
	if !ValidPosition(command.Position) {
		return Receipt{}, Failure("INVALID_POSITION", "节点位置超出允许范围")
	}
	if command.DisplayName != "" {
		if err := validName(command.DisplayName); err != nil {
			return Receipt{}, err
		}
	}
	hash, err := requestHash("registerRepository", id, command)
	if err != nil {
		return Receipt{}, err
	}
	if receipt, found, err := service.replay(ctx, command.MutationID, hash); found || err != nil {
		return receipt, err
	}
	source, err := service.inspector.Inspect(ctx, command.RepositoryPath)
	if err != nil {
		return Receipt{}, err
	}
	if command.DisplayName == "" {
		command.DisplayName = filepath.Base(source.Path)
	}
	if err := validName(command.DisplayName); err != nil {
		return Receipt{}, err
	}
	return service.writer.Register(ctx, id, command, source, hash)
}

// SaveLayout 对全部服务位置进行同一版本下的保存，拒绝重复身份与无效坐标。
func (service *Service) SaveLayout(ctx context.Context, id string, command LayoutCommand) (Receipt, error) {
	if err := validMutation(id, command.MutationID, command.BaseRevision); err != nil {
		return Receipt{}, err
	}
	if len(command.Positions) > 500 {
		return Receipt{}, Failure("INVALID_REQUEST", "服务数量超出当前支持范围")
	}
	seen := make(map[string]bool, len(command.Positions))
	for _, position := range command.Positions {
		if !ValidID(position.ServiceID) || seen[position.ServiceID] || !ValidPosition(Position{X: position.X, Y: position.Y}) {
			return Receipt{}, Failure("INVALID_POSITION", "位置集合包含无效或重复的服务")
		}
		seen[position.ServiceID] = true
	}
	hash, err := requestHash("saveLayout", id, command)
	if err != nil {
		return Receipt{}, err
	}
	if receipt, found, err := service.replay(ctx, command.MutationID, hash); found || err != nil {
		return receipt, err
	}
	return service.writer.SaveLayout(ctx, id, command, hash)
}

// ValidID 判断外部实体标识是否符合发布契约。
func ValidID(value string) bool { return idPattern.MatchString(value) }

// ValidPosition 限制可持久坐标，避免非有限数和过大画布负载。
func ValidPosition(value Position) bool {
	return !math.IsNaN(value.X) && !math.IsNaN(value.Y) && !math.IsInf(value.X, 0) && !math.IsInf(value.Y, 0) && math.Abs(value.X) <= 1000000 && math.Abs(value.Y) <= 1000000
}

// validName 保护显示名称边界，保持中文字符长度语义。
func validName(value string) error {
	if utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 80 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return Failure("INVALID_NAME", "名称应为 1 至 80 个可显示字符")
	}
	return nil
}

// validMutation 校验跨入口均适用的身份和版本前置条件。
func validMutation(id, mutationID, revision string) error {
	if !ValidID(id) || !ValidID(mutationID) {
		return Failure("INVALID_REQUEST", "项目或请求身份无效")
	}
	if _, err := strconv.ParseUint(revision, 10, 64); err != nil || revision == "" {
		return Failure("INVALID_REQUEST", "画布版本无效")
	}
	return nil
}

// requestHash 把请求类型、项目和规范化意图共同纳入幂等约束。
func requestHash(kind, id string, command any) ([32]byte, error) {
	data, err := json.Marshal(struct {
		Kind      string
		ProjectID string
		Command   any
	}{kind, id, command})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(data), nil
}

// replay 区分未受理请求、已持久结果与同键异意图。
func (service *Service) replay(ctx context.Context, id string, hash [32]byte) (Receipt, bool, error) {
	receipt, existing, err := service.receipts.Receipt(ctx, id)
	var failure *Error
	if errors.As(err, &failure) && failure.Code == "MUTATION_NOT_FOUND" {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}
	if existing != hash {
		return Receipt{}, false, Failure("IDEMPOTENCY_CONFLICT", "相同请求身份已用于不同内容，请先核实原结果")
	}
	receipt.Replayed = true
	return receipt, true, nil
}
