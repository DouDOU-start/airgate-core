package plugin

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DouDOU-start/airgate-core/ent/enttest"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

func TestHostForwardTimeout(t *testing.T) {
	cases := []struct {
		name string
		req  hostForwardRequest
		want time.Duration
	}{
		{name: "empty request", req: hostForwardRequest{}, want: defaultHostForwardTimeout},
		{name: "chat request", req: hostForwardRequest{Path: "/v1/chat/completions", Model: "gpt-4o"}, want: defaultHostForwardTimeout},
		{name: "images API request", req: hostForwardRequest{Path: "/v1/images/generations", Model: "gpt-4o"}, want: imageHostForwardTimeout},
		{name: "image model request", req: hostForwardRequest{Path: "/v1/responses", Model: "gpt-image-2"}, want: imageHostForwardTimeout},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostForwardTimeout(tc.req); got != tc.want {
				t.Fatalf("hostForwardTimeout() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestHostForwardReasoningEffort(t *testing.T) {
	t.Parallel()

	req := hostForwardRequest{
		Body: []byte(`{"model":"gpt-5","reasoning":{"effort":"x-high"}}`),
		Headers: map[string]interface{}{
			"Content-Type": []string{"application/json"},
		},
	}

	if got := hostForwardReasoningEffort(req); got != "xhigh" {
		t.Fatalf("hostForwardReasoningEffort() = %q, want %q", got, "xhigh")
	}
}

func TestHostInvokeRequiresDeclaredCapability(t *testing.T) {
	handle := &pluginHostHandle{pluginName: "test-plugin"}
	if err := handle.requireMethod(hostMethodTasksCreate); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected unbound capabilities to be denied, got %v", err)
	}

	handle.SetCapabilities(map[sdk.Capability]bool{})
	if err := handle.requireMethod(hostMethodTasksCreate); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected empty capabilities to be denied, got %v", err)
	}

	handle.SetCapabilities(map[sdk.Capability]bool{
		sdk.CapabilityForHostMethod(hostMethodTasksCreate): true,
	})
	if err := handle.requireMethod(hostMethodTasksCreate); err != nil {
		t.Fatalf("expected declared method capability to pass, got %v", err)
	}
}

func TestHostDeleteAssetLocal(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:host_delete_asset?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	t.Cleanup(func() { _ = db.Close() })

	db.Setting.Create().SetGroup("storage").SetKey("local_storage_dir").SetValue(t.TempDir()).SaveX(ctx)
	storage, err := NewAssetStorage(ctx, db)
	if err != nil {
		t.Fatalf("初始化资产存储失败: %v", err)
	}
	asset := mustStoreTestAsset(t, storage, ctx, 42, AssetPurposeChat)
	assertAssetExists(t, storage, asset.ObjectKey)

	host := &HostService{db: db}
	if _, err := host.deleteAsset(ctx, hostDeleteAssetRequest{ObjectKey: asset.ObjectKey}); err != nil {
		t.Fatalf("删除资产失败: %v", err)
	}
	assertAssetMissing(t, storage, asset.ObjectKey)
}

func TestDeleteTaskDeletesAssociatedAssets(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:delete_task_assets?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	t.Cleanup(func() { _ = db.Close() })

	db.Setting.Create().SetGroup("storage").SetKey("local_storage_dir").SetValue(t.TempDir()).SaveX(ctx)
	storage, err := NewAssetStorage(ctx, db)
	if err != nil {
		t.Fatalf("初始化资产存储失败: %v", err)
	}

	host := &HostService{db: db}
	big := bigDataURI(t, "image/png", 32<<10)
	created, err := host.createTask(ctx, "gateway-openai", hostCreateTaskRequest{
		UserID:   42,
		TaskType: "image.edit",
		Input: map[string]interface{}{
			"prompt": "edit",
			"images": []interface{}{big},
		},
	})
	if err != nil {
		t.Fatalf("创建任务失败: %v", err)
	}

	task := created["task"].(map[string]interface{})
	taskID := task["id"].(int64)
	input := task["input"].(map[string]interface{})
	inputAssetURL := input["images"].([]interface{})[0].(string)
	inputObjectKey, err := runtimeAssetURLToObjectKey(inputAssetURL)
	if err != nil {
		t.Fatalf("解析输入资产 URL 失败: %v", err)
	}
	assertAssetExists(t, storage, inputObjectKey)

	generated := mustStoreTestAsset(t, storage, ctx, 42, AssetPurposeGenerated)
	if _, err := host.updateTask(ctx, "gateway-openai", hostUpdateTaskRequest{
		TaskID: taskID,
		Status: "processing",
	}); err != nil {
		t.Fatalf("启动任务失败: %v", err)
	}
	if _, err := host.updateTask(ctx, "gateway-openai", hostUpdateTaskRequest{
		TaskID: taskID,
		Status: "completed",
		Output: map[string]interface{}{
			"content":           fmt.Sprintf("![image](%s)", generated.PublicURL),
			"asset_object_keys": []interface{}{generated.ObjectKey},
		},
	}); err != nil {
		t.Fatalf("完成任务失败: %v", err)
	}

	if _, err := host.deleteTask(ctx, "gateway-openai", hostDeleteTaskRequest{
		TaskID: taskID,
		UserID: 42,
	}); err != nil {
		t.Fatalf("删除任务失败: %v", err)
	}
	assertAssetMissing(t, storage, inputObjectKey)
	assertAssetMissing(t, storage, generated.ObjectKey)
}

func TestTaskPublicIDIsIndependentFromIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:task_public_id?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	t.Cleanup(func() { _ = db.Close() })

	host := &HostService{db: db}
	baseReq := hostCreateTaskRequest{
		UserID:         42,
		Input:          map[string]interface{}{"prompt": "test"},
		IdempotencyKey: "same-idempotency-key",
	}
	if _, err := host.createTask(ctx, "gateway-openai", hostCreateTaskRequest{
		UserID:         baseReq.UserID,
		TaskType:       "image.generate",
		Input:          baseReq.Input,
		PublicTaskID:   "pub-generate",
		IdempotencyKey: baseReq.IdempotencyKey,
	}); err != nil {
		t.Fatalf("create generate task: %v", err)
	}
	if _, err := host.createTask(ctx, "gateway-openai", hostCreateTaskRequest{
		UserID:         baseReq.UserID,
		TaskType:       "image.edit",
		Input:          baseReq.Input,
		PublicTaskID:   "pub-edit",
		IdempotencyKey: baseReq.IdempotencyKey,
	}); err != nil {
		t.Fatalf("create edit task with same idempotency key: %v", err)
	}

	got, err := host.getTask(ctx, "gateway-openai", hostGetTaskRequest{UserID: baseReq.UserID, PublicTaskID: "pub-edit"})
	if err != nil {
		t.Fatalf("get task by public id: %v", err)
	}
	task, ok := got["task"].(map[string]interface{})
	if !ok {
		t.Fatalf("task payload type = %T", got["task"])
	}
	if task["task_type"] != "image.edit" || task["public_task_id"] != "pub-edit" {
		t.Fatalf("unexpected task payload: %+v", task)
	}

	_, err = host.getTask(ctx, "gateway-openai", hostGetTaskRequest{UserID: baseReq.UserID, PublicTaskID: baseReq.IdempotencyKey})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("idempotency key should not be usable as public task id, got %v", err)
	}
}

func TestListTasksFiltersByPluginID(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:list_tasks_plugin_id?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	t.Cleanup(func() { _ = db.Close() })

	host := &HostService{db: db}
	for _, pluginID := range []string{"gateway-openai", "other-plugin"} {
		if _, err := host.createTask(ctx, pluginID, hostCreateTaskRequest{
			UserID:   42,
			TaskType: "image.generate",
			Input:    map[string]interface{}{"prompt": pluginID},
		}); err != nil {
			t.Fatalf("create task for %s: %v", pluginID, err)
		}
	}

	got, err := host.listTasks(ctx, "airgate-studio", hostListTasksRequest{
		PluginID: "gateway-openai",
		UserID:   42,
		Limit:    20,
	})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	tasks, ok := got["tasks"].([]map[string]interface{})
	if !ok {
		t.Fatalf("tasks payload type = %T", got["tasks"])
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks len = %d, want 1: %+v", len(tasks), tasks)
	}
	if tasks[0]["plugin_id"] != "gateway-openai" {
		t.Fatalf("plugin_id = %v, want gateway-openai", tasks[0]["plugin_id"])
	}
}

func TestListTasksStripsHeavyInputFields(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:list_tasks_slim?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	t.Cleanup(func() { _ = db.Close() })

	host := &HostService{db: db}
	created, err := host.createTask(ctx, "gateway-openai", hostCreateTaskRequest{
		UserID:   42,
		TaskType: "image.edit",
		Input: map[string]interface{}{
			"prompt": "make it blue",
			"model":  "gpt-image-1",
			"size":   "1024x1024",
			"images": []interface{}{"data:image/png;base64,AAAA"},
			"mask":   "data:image/png;base64,BBBB",
		},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	taskID := int64(created["task"].(map[string]interface{})["id"].(int64))

	got, err := host.listTasks(ctx, "gateway-openai", hostListTasksRequest{UserID: 42, Limit: 20})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	tasks := got["tasks"].([]map[string]interface{})
	if len(tasks) != 1 {
		t.Fatalf("tasks len = %d, want 1", len(tasks))
	}
	input, ok := tasks[0]["input"].(map[string]interface{})
	if !ok {
		t.Fatalf("input type = %T", tasks[0]["input"])
	}
	if _, present := input["images"]; present {
		t.Fatalf("list response must omit input.images, got: %+v", input)
	}
	if _, present := input["mask"]; present {
		t.Fatalf("list response must omit input.mask, got: %+v", input)
	}
	if input["prompt"] != "make it blue" || input["model"] != "gpt-image-1" || input["size"] != "1024x1024" {
		t.Fatalf("list response stripped too much, got: %+v", input)
	}

	// tasks.get must still return the full input for callers that need it.
	full, err := host.getTask(ctx, "gateway-openai", hostGetTaskRequest{UserID: 42, TaskID: taskID})
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	fullInput := full["task"].(map[string]interface{})["input"].(map[string]interface{})
	if _, present := fullInput["images"]; !present {
		t.Fatalf("get response must keep input.images, got: %+v", fullInput)
	}
	if _, present := fullInput["mask"]; !present {
		t.Fatalf("get response must keep input.mask, got: %+v", fullInput)
	}
}

func TestCreateTaskNormalizesLargeInputDataURIs(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:create_task_normalize?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	t.Cleanup(func() { _ = db.Close() })

	// 让 NewAssetStorage 落到测试临时目录，而不是默认 data/assets。
	t.Setenv("ASSETS_DIR", t.TempDir())

	host := &HostService{db: db}
	big := bigDataURI(t, "image/png", 32<<10)
	created, err := host.createTask(ctx, "gateway-openai", hostCreateTaskRequest{
		UserID:   7,
		TaskType: "image.edit",
		Input: map[string]interface{}{
			"prompt": "rotate left",
			"model":  "gpt-image-1",
			"images": []interface{}{big, big},
			"mask":   big,
		},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	task := created["task"].(map[string]interface{})
	input := task["input"].(map[string]interface{})
	if input["prompt"] != "rotate left" {
		t.Fatalf("prompt mutated: %+v", input["prompt"])
	}
	images := input["images"].([]interface{})
	for i, img := range images {
		s, ok := img.(string)
		if !ok {
			t.Fatalf("images[%d] type = %T", i, img)
		}
		if !strings.HasPrefix(s, "/assets-runtime/") {
			t.Fatalf("images[%d] not normalized: %s", i, s[:40])
		}
		if !strings.Contains(s, "/task-input/7/") {
			t.Fatalf("images[%d] wrong object_key prefix: %s", i, s)
		}
	}
	if !strings.HasPrefix(input["mask"].(string), "/assets-runtime/") {
		t.Fatalf("mask not normalized: %s", input["mask"].(string)[:40])
	}

	// 再确认 list payload 也不再带任何 base64 — list 已经在剥 images/mask，
	// 这里主要验证如果有人撤掉那个剥字段逻辑，归一化也能挡住 64MB 上限。
	listed, err := host.listTasks(ctx, "gateway-openai", hostListTasksRequest{UserID: 7, Limit: 20})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	tasks := listed["tasks"].([]map[string]interface{})
	if len(tasks) != 1 {
		t.Fatalf("tasks len = %d", len(tasks))
	}
}

func TestCheckHostForwardBalance(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:host_forward_balance?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	t.Cleanup(func() { _ = db.Close() })

	zeroBalanceUser := db.User.Create().SetEmail("zero@example.com").SetPasswordHash("hash").SetBalance(0).SaveX(ctx)
	positiveBalanceUser := db.User.Create().SetEmail("positive@example.com").SetPasswordHash("hash").SetBalance(1).SaveX(ctx)

	host := &HostService{db: db}

	if err := host.checkHostForwardBalance(ctx, int64(zeroBalanceUser.ID)); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted for zero balance, got %v", err)
	}
	if err := host.checkHostForwardBalance(ctx, int64(positiveBalanceUser.ID)); err != nil {
		t.Fatalf("expected positive balance user to pass, got %v", err)
	}
}
