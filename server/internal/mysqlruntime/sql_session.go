// 本文件只供受信数据库管理器执行已校验的SQL，业务迁移不使用管理身份。
package mysqlruntime

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/hailingu/twine-realm/server/internal/localdocker"
)

// sqlTransport 将并发取消与串行协议、回收分开，取消不得并发读取或改变协议状态。
type sqlTransport interface {
	io.ReadWriteCloser
	Interrupt()
}

// sqlSession 串行拥有一个管理会话，错误只保留安全诊断而不保存SQL或进程原文。
type sqlSession struct {
	transport sqlTransport
	scanner   *bufio.Scanner
	prefix    string
	sequence  uint64
	failed    error
}

// newSQLSession 为每条请求加独立边界，拒绝启动横幅、错序结果和未确认的尾部。
func newSQLSession(transport sqlTransport) (*sqlSession, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return nil, errors.Join(failure("DATABASE_SESSION_UNAVAILABLE", "无法建立受控数据库会话", false, false), sqlTransportFailure(transport.Close()))
	}
	scanner := bufio.NewScanner(transport)
	scanner.Buffer(make([]byte, 4096), 128*1024)
	return &sqlSession{transport: transport, scanner: scanner, prefix: "__TWINE_SQL_" + hex.EncodeToString(data)}, nil
}

// databaseSQL 只连接已核实资源的固定容器，不把管理秘密放进argv或环境。
func (driver *Driver) databaseSQL(ctx context.Context, resource Resource, containerID string) (*sqlSession, error) {
	return driver.databaseSQLWithTimeout(ctx, resource, containerID, 120*time.Second)
}

// databaseSQLWithTimeout 只允许受信消费者选择有界会话期限，不改变固定容器与秘密边界。
func (driver *Driver) databaseSQLWithTimeout(ctx context.Context, resource Resource, containerID string, timeout time.Duration) (*sqlSession, error) {
	if err := driver.validateResource(resource); err != nil {
		return nil, err
	}
	if !dockerIdentity.MatchString(containerID) {
		return nil, failure("RUNTIME_PLAN_INVALID", "数据库会话缺少已核实的容器身份", false, false)
	}
	if err := driver.verifySecrets(resource); err != nil {
		return nil, err
	}
	fact, err := driver.Inspect(ctx, resource)
	if err != nil {
		return nil, err
	}
	if fact == nil || !fact.Ready || fact.ContainerID != containerID {
		return nil, failure("RUNTIME_OWNERSHIP_CONFLICT", "管理会话目标不是当前已认证的固定数据库容器", false, false)
	}
	transport, err := localdocker.OpenSession(ctx, localdocker.Policy{Timeout: timeout}, contextOption, driver.Environment().Context, "exec", "--interactive", "--user", "0:0", containerID, "mysql", "--defaults-file=/run/secrets/client_options", "--batch", "--skip-column-names", "--raw", "--silent", "--unbuffered", "--skip-auto-rehash")
	if err != nil {
		return nil, sqlTransportFailure(err)
	}
	return newSQLSession(transport)
}

// query 仅接收平台生成的单条SQL；业务脚本必须由独立迁移任务使用受限账号执行。
func (session *sqlSession) query(ctx context.Context, sql string) ([]string, error) {
	if session.failed != nil {
		return nil, session.failed
	}
	if ctx.Err() != nil {
		return nil, session.cancelled()
	}
	stop := context.AfterFunc(ctx, session.transport.Interrupt)
	defer stop()
	if len(sql) == 0 || len(sql) > 8192 || strings.ContainsAny(sql, ";\x00") || session.sequence >= 1000 {
		return nil, session.stop(failure("DATABASE_QUERY_INVALID", "数据库管理请求超出受控边界", false, false))
	}
	session.sequence++
	base := session.prefix + "_" + strconv.FormatUint(session.sequence, 10)
	start, end := base+"_START__", base+"_END__"
	input := "SELECT '" + start + "';\n" + sql + ";\nSHOW COUNT(*) WARNINGS;\nSELECT '" + end + "';\n"
	n, err := io.WriteString(session.transport, input)
	if err != nil || n != len(input) {
		return nil, session.stop(failure("DATABASE_RESULT_UNKNOWN", "数据库请求传输未完成，须先核实原结果", true, true))
	}
	if !session.scanner.Scan() || session.scanner.Text() != start {
		if ctx.Err() != nil {
			return nil, session.cancelled()
		}
		return nil, session.protocolFailure()
	}
	return session.rows(ctx, end)
}

// rows 只接受当前边界内的有限结果，所有SQL告警都阻断后续写入。
func (session *sqlSession) rows(ctx context.Context, end string) ([]string, error) {
	values := []string{}
	for session.scanner.Scan() {
		if ctx.Err() != nil {
			return nil, session.cancelled()
		}
		line := session.scanner.Text()
		if line == end {
			return session.checkedRows(values)
		}
		if len(values) >= 1024 {
			return nil, session.protocolFailure()
		}
		values = append(values, line)
	}
	if ctx.Err() != nil {
		return nil, session.cancelled()
	}
	return nil, session.protocolFailure()
}

// checkedRows 只有边界和告警计数均完整时才交付结果，不将告警当成成功。
func (session *sqlSession) checkedRows(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, session.protocolFailure()
	}
	warnings, err := strconv.ParseUint(values[len(values)-1], 10, 32)
	if err != nil {
		return nil, session.protocolFailure()
	}
	if warnings != 0 {
		return nil, session.stop(failure("DATABASE_WARNING", "数据库操作报告告警，保留原结果并等待核实", false, true))
	}
	return values[:len(values)-1], nil
}

// cancelled 停止原通道并保留未知结果，后续不能在同一连接补发写入。
func (session *sqlSession) cancelled() error {
	session.transport.Interrupt()
	return session.stop(failure("DATABASE_RESULT_UNKNOWN", "数据库请求已取消，须先核实原结果", true, true))
}

// protocolFailure 不转储超限、截断或带敏感内容的原始响应。
func (session *sqlSession) protocolFailure() error {
	return session.stop(failure("DATABASE_PROTOCOL_INVALID", "数据库响应边界未确认，须先核实原结果", false, true))
}

// stop 保留第一次失败，禁止在未知会话里继续发送写入。
func (session *sqlSession) stop(err error) error {
	if session.failed == nil {
		session.failed = err
	}
	return session.failed
}

// Close 回收传输并保留此前失败，原始子进程错误也不能绕过安全诊断边界。
func (session *sqlSession) Close() error {
	return errors.Join(session.failed, sqlTransportFailure(session.transport.Close()))
}

// sqlTransportFailure 保留告警类别，但不传播可能包含秘密的原始传输错误。
func sqlTransportFailure(err error) error {
	if err == nil {
		return nil
	}
	var fault *localdocker.Fault
	if errors.As(err, &fault) && fault.Code == "DOCKER_WARNING" {
		return failure("DATABASE_WARNING", "数据库管理通道报告告警，须处理后核实", false, true)
	}
	return failure("DATABASE_RESULT_UNKNOWN", "数据库管理通道未确认，须先核实原结果", true, true)
}
