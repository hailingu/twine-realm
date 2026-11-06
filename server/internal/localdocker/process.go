// 本文件让一次命令与受控交互会话共用期限、Docker环境及子进程边界。
package localdocker

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// dockerProcess 只创建受控进程描述，不启动命令或继承业务环境。
func dockerProcess(ctx context.Context, policy Policy, args []string) (*exec.Cmd, error) {
	if policy.Timeout <= 0 || policy.Timeout > 10*time.Minute {
		return nil, failure("DOCKER_POLICY_INVALID", "Docker命令必须具有不超过十分钟的明确期限", false, false)
	}
	for _, key := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"} {
		if os.Getenv(key) != "" {
			return nil, failure("DOCKER_OVERRIDE_UNSUPPORTED", "本切片不接受Docker环境覆盖，请用本地context明确初始化", false, false)
		}
	}
	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = []string{}
	for _, key := range []string{"PATH", "HOME", "TMPDIR"} {
		if value, present := os.LookupEnv(key); present {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	if policy.BuildMetadata {
		command.Env = append(command.Env, "BUILDX_METADATA_WARNINGS=true", "BUILDX_METADATA_PROVENANCE=min")
	}
	command.WaitDelay = 2 * time.Second
	return command, nil
}
