// 本文件验证原构建恢复所需的私有证据；不把可读取容器当成重跑业务代码的授权。
package servicebuild

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// recoveryBoundary 模拟已核实的原资源，精确记录停止/移除，不允许运行源码或创建替代实例。
type recoveryBoundary struct {
	driver    *Driver
	box       sandbox
	container containerState
	present   bool
	volume    *outputVolume
	mutations []string
	fail      string
}

// newRecoveryBoundary 为每个故障建立独立私有根和一致的原输入摘要。
func newRecoveryBoundary(t *testing.T) *recoveryBoundary {
	t.Helper()
	driver, box, value := sandboxFixture(t)
	var err error
	box.target.InputDigest, err = InputFingerprint(box.target.Source, box.target.Profile, box.target.Environment, box.target.Policy, box.target.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	value.Config.Labels = box.labels()
	boundary := &recoveryBoundary{driver: driver, box: box, container: value, present: true}
	driver.execute = boundary.execute
	return boundary
}

// execute 保持同一工具环境，只接受本测试明确声明的资源读取和收回命令。
func (boundary *recoveryBoundary) execute(ctx context.Context, duration time.Duration, metadata bool, args ...string) ([]byte, error) {
	command := strings.Join(args, " ")
	if boundary.fail != "" && strings.Contains(command, boundary.fail) {
		return nil, errors.New("受控原资源读取失败")
	}
	if command == "--context local-test info --format {{json .ID}}" {
		return json.Marshal(boundary.driver.Environment().DaemonID)
	}
	if len(args) >= 4 {
		switch args[2] {
		case "container":
			return boundary.containerCommand(args[3:])
		case "volume":
			return boundary.volumeCommand(args[3:])
		}
	}
	return fakeEnvironmentCommand(ctx, duration, metadata, args...)
}

// containerCommand 只模拟原对象的读、停止和移除；不能执行任意业务命令。
func (boundary *recoveryBoundary) containerCommand(args []string) ([]byte, error) {
	switch args[0] {
	case "ls":
		if boundary.present {
			return json.Marshal(map[string]string{"Names": boundary.box.name})
		}
		return nil, nil
	case "inspect":
		return json.Marshal([]containerState{boundary.container})
	case "unpause":
		boundary.container.State.Paused = false
	case "stop":
		boundary.container.State.Running, boundary.container.State.Status = false, "exited"
	case "rm":
		boundary.present = false
	default:
		return nil, errors.New("不允许创建、启动或执行业务源码")
	}
	boundary.mutations = append(boundary.mutations, args[0])
	return nil, nil
}

// volumeCommand 只允许回收原输出卷，缺少容器不等于可以删除其他卷。
func (boundary *recoveryBoundary) volumeCommand(args []string) ([]byte, error) {
	switch args[0] {
	case "ls":
		if boundary.volume != nil {
			return json.Marshal(map[string]string{"Name": boundary.volume.Name})
		}
		return nil, nil
	case "inspect":
		return json.Marshal([]*outputVolume{boundary.volume})
	case "rm":
		boundary.mutations = append(boundary.mutations, "volume-rm")
		boundary.volume = nil
		return nil, nil
	default:
		return nil, errors.New("不允许创建替代输出卷")
	}
}

// writeRecoveryProof 写入实际私有目录，错误内容仍由生产解码器判定。
func writeRecoveryProof(t *testing.T, boundary *recoveryBoundary, data []byte) {
	t.Helper()
	name := "operations/" + boundary.box.operationID
	if err := boundary.driver.files.Mkdir(name); err != nil {
		t.Fatal(err)
	}
	if data != nil {
		if err := boundary.driver.files.WriteNew(name+"/target.json", data); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRecoveryRequiresOriginalProof 丢失、损坏或别的目标材料均不得停止一个看似归属相符的容器。
func TestRecoveryRequiresOriginalProof(t *testing.T) {
	for _, mode := range []string{"missing", "broken", "foreign", "invalid-target", "preflight", "inspect"} {
		t.Run(mode, func(t *testing.T) {
			boundary := newRecoveryBoundary(t)
			target := boundary.box.target
			var data []byte
			switch mode {
			case "broken":
				data = []byte(`{"unknown":true}`)
			case "foreign":
				copy := target
				copy.Generation = "99"
				data, _ = json.Marshal(copy)
			case "invalid-target":
				target.InputDigest = ContentDigest(nil)
			case "preflight":
				boundary.fail = "buildx version"
			case "inspect":
				boundary.fail = "container inspect"
			}
			writeRecoveryProof(t, boundary, data)
			if err := boundary.driver.Recover(context.Background(), target, boundary.box.operationID); err == nil || len(boundary.mutations) != 0 || !boundary.present {
				t.Fatal("恢复未核实原证据就执行资源变更")
			}
		})
	}
}

// TestRecoveryOnlyReclaimsOriginalResources 重启恢复收回暂停的原对象，不重跑Go、创建容器或发布候选。
func TestRecoveryOnlyReclaimsOriginalResources(t *testing.T) {
	boundary := newRecoveryBoundary(t)
	boundary.container.State.Paused = true
	data, err := json.Marshal(boundary.box.target)
	if err != nil {
		t.Fatal(err)
	}
	writeRecoveryProof(t, boundary, data)
	if err := boundary.driver.Recover(context.Background(), boundary.box.target, boundary.box.operationID); err != nil {
		t.Fatal(err)
	}
	if boundary.present || strings.Join(boundary.mutations, ",") != "unpause,stop,rm" {
		t.Fatal("原容器未按核实顺序收回")
	}
	before := len(boundary.mutations)
	if err := boundary.driver.Recover(context.Background(), boundary.box.target, boundary.box.operationID); err != nil || len(boundary.mutations) != before {
		t.Fatal("重复核实产生第二次执行")
	}
}

// TestOrphanOutputVolumeOwnership 容器消失后只能移除完整核实的本次临时输出卷，不能接管外来卷。
func TestOrphanOutputVolumeOwnership(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "foreign"}[foreign], func(t *testing.T) {
			boundary := newRecoveryBoundary(t)
			boundary.present = false
			boundary.volume = &outputVolume{Name: boundary.box.name + "-out", Driver: "local", CreatedAt: "2026-11-06T00:00:00Z", Labels: boundary.box.labels(), Options: boundary.box.volumeOptions()}
			if foreign {
				boundary.volume.Options["device"] = "/user-data"
			}
			err := boundary.driver.Recover(context.Background(), boundary.box.target, boundary.box.operationID)
			if foreign {
				if err == nil || boundary.volume == nil || len(boundary.mutations) != 0 {
					t.Fatal("外来输出卷被接管或删除")
				}
			} else if err != nil || boundary.volume != nil || strings.Join(boundary.mutations, ",") != "volume-rm" {
				t.Fatal("本次孤立输出卷未核实收回")
			}
		})
	}
}
