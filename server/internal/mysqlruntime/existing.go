// 本文件只读取和改变已存在的原数据库，不生成材料或资源。
package mysqlruntime

import (
	"bytes"
	"context"
	"time"
)

// ExistingIdentity 是冻结数据库的物理身份，调用者必须持有项目文件锁。
type ExistingIdentity struct{ ContainerID, NetworkID, VolumeCreatedAt string }

// InspectExisting 严格要求原资源、秘密与Compose材料完整存在。
func (driver *Driver) InspectExisting(ctx context.Context, resource Resource) (ExistingIdentity, *Observation, error) {
	image, err := driver.preflight(ctx, resource)
	if err != nil {
		return ExistingIdentity{}, nil, err
	}
	if err := driver.verifySecrets(resource); err != nil {
		return ExistingIdentity{}, nil, err
	}
	expected, err := driver.composeMaterial(resource)
	if err != nil {
		return ExistingIdentity{}, nil, err
	}
	actual, err := driver.readPrivate(projectFilePrefix+resource.ID+"/compose.json", 32768)
	if err != nil || !bytes.Equal(actual, expected) {
		return ExistingIdentity{}, nil, failure("RUNTIME_MATERIAL_CONFLICT", "原数据库材料缺失或被改写", false, false)
	}
	state, err := driver.inspect(ctx, resource, image)
	if err != nil {
		return ExistingIdentity{}, nil, err
	}
	if state.container == nil || state.network == nil || state.volume == nil {
		return ExistingIdentity{}, nil, failure("RUNTIME_RESOURCE_MISSING", "原数据库容器、网络或卷缺失，禁止补建", false, false)
	}
	fact := &Observation{ContainerID: state.container.ID, Running: state.container.State.Running, VolumeFound: true, State: "stopped", ObservedAt: Timestamp(time.Now())}
	if fact.Running {
		fact.State = "running"
	}
	return ExistingIdentity{state.container.ID, state.network.ID, state.volume.CreatedAt}, fact, nil
}

// ChangeExisting 只对已核实的原容器ID发出一个动作；外部调用者负责动作前后持久账本。
func (driver *Driver) ChangeExisting(ctx context.Context, resource Resource, expected ExistingIdentity, running bool) error {
	current, fact, err := driver.InspectExisting(ctx, resource)
	if err != nil {
		return err
	}
	if current != expected || expected.ContainerID == "" || expected.NetworkID == "" {
		return failure("RUNTIME_IDENTITY_CONFLICT", "原数据库物理身份已改变", false, false)
	}
	if fact.Running == running {
		return nil
	}
	action := "stop"
	if running {
		action = "start"
	}
	_, err = driver.execute(ctx, contextOption, driver.Environment().Context, action, current.ContainerID)
	return err
}

// VerifyExistingBindingMaterials 在数据库停止时只读核实原业务秘密及固定字节。
func (driver *Driver) VerifyExistingBindingMaterials(ctx context.Context, request BindingRequest, data []byte) error {
	if err := driver.validateBinding(request); err != nil {
		return err
	}
	session := &BindingSession{driver: driver, request: request, prefix: "projects/" + request.Resource.ID + "/services/" + request.ServiceID + "/"}
	if err := session.bindingSecrets(ctx, true); err != nil {
		return err
	}
	_, err := driver.composeExtensionFile(ctx, request.Resource, ComposeOverlay{ServiceID: request.ServiceID, Data: data})
	return err
}
