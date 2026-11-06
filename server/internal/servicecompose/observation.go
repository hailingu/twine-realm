// 本文件通过原容器、原数据库历史和回环HTTP只读探针采集运行事实。
package servicecompose

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// Inspect 不调用Prepare补建对象，原秘密或身份缺失时保留最后事实并显式报错。
func (driver *Driver) Inspect(ctx context.Context, target servicedeploy.Target) (fact *servicedeploy.Observation, err error) {
	if err := driver.build.VerifyCandidate(ctx, target.Plan.Candidate); err != nil {
		return nil, err
	}
	request, err := driver.bindingRequest(ctx, target.Plan)
	if err != nil {
		return nil, err
	}
	err = driver.runtime.WithBinding(ctx, request, func(session *mysqlruntime.BindingSession) error {
		if err := session.Verify(ctx); err != nil {
			return err
		}
		var err error
		fact, err = driver.inspectFacts(ctx, target.Plan, session)
		return err
	})
	return fact, err
}

// inspectFacts 不让单个200响应替代容器归属、数据库UUID和迁移内容证明。
func (driver *Driver) inspectFacts(ctx context.Context, plan servicedeploy.Plan, session *mysqlruntime.BindingSession) (*servicedeploy.Observation, error) {
	fact := &servicedeploy.Observation{State: "not_created", MigrationIntegrity: "unknown", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	value, err := driver.inspectContainer(ctx, plan, "application", session.Files())
	if err != nil {
		return nil, err
	}
	if value == nil {
		return fact, nil
	}
	fact.ContainerID, fact.ImageID, fact.Running = value.ID, value.Image, value.State.Running
	fact.State = "stopped"
	if fact.Running {
		fact.State = "running"
	}
	expected, err := migrationDigests(plan.Candidate)
	if err != nil {
		return fact, err
	}
	history, err := session.History(ctx, expected)
	if err != nil {
		return fact, err
	}
	fact.DatabaseUUID, fact.MigrationVersion, fact.MigrationIntegrity = plan.Database.ServerUUID, history.Version, history.Integrity
	if !fact.Running {
		return fact, nil
	}
	entry := "http://127.0.0.1:" + strconv.Itoa(plan.Request.HostPort)
	fact.Healthy = probe(ctx, entry+"/livez", "alive")
	fact.Ready = fact.Healthy && history.Integrity == "verified" && probe(ctx, entry+"/readyz", "ready")
	if fact.Ready {
		fact.State, fact.EntryURL = "ready", entry
	}
	return fact, nil
}

// probe 禁止代理、重定向和无界响应，只接受固定只读健康响应。
func probe(ctx context.Context, address, status string) bool {
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 1025))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(data) > 1024 || response.StatusCode != http.StatusOK {
		return false
	}
	var value map[string]string
	return json.Unmarshal(data, &value) == nil && len(value) == 1 && value["status"] == status
}
