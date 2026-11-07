// 本文件只拥有连接计数，不在持锁时做网络或数据库 I/O。
package httpapi

import (
	"sync"

	"github.com/hailingu/twine-realm/server/internal/projectstate"
)

// stateConnections 限制实际活动流；每个成功领取的请求恰好释放一次。
type stateConnections struct {
	mutex    sync.Mutex
	total    int
	projects map[string]int
}

// acquire 在固定全局及项目界限内领取，不预先创建无用的项目计数。
func (connections *stateConnections) acquire(id string) bool {
	connections.mutex.Lock()
	defer connections.mutex.Unlock()
	policy := projectstate.DefaultPolicy()
	if connections.total >= policy.MaxConnections || connections.projects[id] >= policy.MaxProjectConnections {
		return false
	}
	connections.total++
	connections.projects[id]++
	return true
}

// release 由请求拥有者的 defer 调用，空项目立即移出计数表。
func (connections *stateConnections) release(id string) {
	connections.mutex.Lock()
	defer connections.mutex.Unlock()
	connections.total--
	connections.projects[id]--
	if connections.projects[id] == 0 {
		delete(connections.projects, id)
	}
}
