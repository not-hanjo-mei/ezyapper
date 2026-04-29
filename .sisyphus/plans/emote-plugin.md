# 表情包插件实现计划

## TL;DR

> **快速摘要**: 插件在 `OnMessage` 里自动检测图片 → Vision分析 → 决定存储。LLM暴露3个查询工具。核心只改一行结构体 + 3行转换函数。
> 
> **交付物**: DiscordMessage 扩展 + 完整插件（OnMessage 偷取 + 3个LLM工具）
> **预估工作量**: Medium
> **并行执行**: YES - 2 waves

---

## Context

### Original Request
1. 全局表情库 + 群组专属表情
2. 从对话中"偷取"表情（**插件自己决定**，不经过主LLM）
3. 表情搜索/分类
4. 不重复存储、黑白名单过滤

### Key Decisions
| 决策 | 选择 |
|------|------|
| 偷取触发 | `OnMessage` 自动检测，插件内 Vision 分析决定 |
| LLM 工具 | 3个查询工具，不含 steal |
| 核心修改 | `DiscordMessage` + `AttachmentURLs`（一行） + `FromDiscordgo`（3行） |
| 存储 | 本地文件，按群组分目录 |
| 元数据 | 单个 JSON 文件/群组 |
| Vision | 插件自配 API 密钥 |
| 手动添加 | 修改文件实现 |

### Metis Review（已解决）
1. ~~DiscordMessage无附件字段~~ → 加 `AttachmentURLs []string`，omitempty，向后兼容
2. ~~插件无法访问Vision Model~~ → 插件配置自己的API密钥
3. 并发写入安全 → 原子写入（temp → rename）
4. 重复存储 → SHA256 内容哈希去重
5. 过度调用 → 黑白名单 + 频率限制

---

## Work Objectives

### Core Objective
插件在 `OnMessage` 中自动检测图片附件，调用 Vision 分析决定是否存储。3个LLM工具用于查询。

### Deliverables
- `internal/plugin/plugin.go` — DiscordMessage 加一个字段
- `plugins/emote-plugin/main.go` — 插件入口
- `plugins/emote-plugin/tools.go` — 3个查询工具
- `plugins/emote-plugin/vision.go` — Vision 集成
- `plugins/emote-plugin/storage.go` — 存储 + 原子写入
- `plugins/emote-plugin/config.go` — 配置
- `plugins/emote-plugin/config.yaml` — 配置模板
- `plugins/emote-plugin/README.md` — 文档
- `plugins/emote-plugin/*_test.go` — 测试

### 3 个 LLM 工具

| 工具 | 调用场景 | 参数 |
|------|---------|------|
| `list_emotes` | LLM想展示可选表情 | guild_id?, limit? |
| `search_emote` | LLM根据用户描述找表情 | query, guild_id?, limit? |
| `get_emote` | LLM需要表情详细信息 | id 或 name |

### OnMessage 自动偷取流程（不经过主LLM）

```
Discord 消息到达
    ↓
bot → PluginManager.OnMessage(discordMsg)   # DiscordMessage 现在有 AttachmentURLs
    ↓
插件 OnMessage:
    for each url in msg.AttachmentURLs:
        if isImageContentType(url):
            检查黑白名单 → 拒绝 or 继续
            下载图片
            SHA256去重 → 跳过 or 继续
            Vision分析 → 不是表情包？跳过
            生成 name/tags/description
            原子写入保存 + 更新 metadata.json
    return true  # 不拦截正常消息处理
```

### Must Have
- [ ] DiscordMessage 添加 `AttachmentURLs []string`
- [ ] OnMessage 自动偷取（Vision决定，主LLM不参与）
- [ ] SHA256 去重 + 黑白名单 + 频率限制
- [ ] 3个LLM查询工具
- [ ] 原子写入

### Must NOT Have
- 不修改 DiscordMessage 其他字段
- 不使用 Qdrant / SQLite
- 不支持动画 GIF（v1）
- 不实现 `add_emote` 工具（手动改文件添加）
- 不添加 WebUI / 使用统计 / 导入导出
- 不在被禁 server/channel/user 上偷取

---

## Verification Strategy

### Test Decision
- **Infrastructure**: YES (go test)
- **Automated tests**: YES (TDD)
- **Framework**: go test (standard library)

### QA Policy
Every task includes agent-executed QA scenarios.
Evidence: `.sisyphus/evidence/task-{N}-{slug}.{ext}`

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1（启动 — 基础设施）:
├── Task 1: 修改DiscordMessage添加附件字段     [quick]
├── Task 2: 创建插件目录结构                    [quick]
├── Task 3: 插件框架 + 配置加载                 [unspecified-high]
├── Task 4: 存储层（原子写入+去重+黑白名单）     [unspecified-high]
└── Task 5: OnMessage自动偷取（核心）           [deep]

Wave 2（依赖 Wave 1 — 工具 + 测试）:
├── Task 6: list_emotes 工具                   [unspecified-high]
├── Task 7: search_emote 工具                  [unspecified-high]
├── Task 8: get_emote 工具                     [unspecified-high]
├── Task 9: 配置验证 + 错误处理                 [unspecified-high]
├── Task 10: 单元测试                          [unspecified-high]
└── Task 11: 集成测试 + 文档                    [unspecified-high]

Wave FINAL（4 并行审查）:
├── F1: Plan compliance audit (oracle)
├── F2: Code quality review (unspecified-high)
├── F3: Real manual QA (unspecified-high)
└── F4: Scope fidelity check (deep)
```

### Dependency Matrix

- **1-5**: — — 6-11, 1
- **6-9**: 3,4 — 10-11, 2
- **10-11**: 4-9 — F1-F4, 2

---

## TODOs

- [x] 1. 修改 DiscordMessage 添加附件字段

  **What**: 在 `DiscordMessage` 加一行 `AttachmentURLs []string \`json:"attachment_urls,omitempty"\``，`FromDiscordgo()` 中提取 `m.Attachments` 的 URL（仅 image/ 开头的 ContentType）。不改其他字段。
  
  **Category**: `quick` | **Parallel**: Wave 1 (with 2-5)
  **Refs**: `internal/plugin/plugin.go:119-142`

  **QA**:
  ```
  go test ./internal/plugin/ -run TestDiscordMessage_AttachmentURLs → PASS
  go test ./internal/plugin/ -v → 全部PASS（向后兼容）
  ```
  **Evidence**: task-1-attachment.txt, task-1-compat.txt
  **Commit**: `feat(plugin): add AttachmentURLs to DiscordMessage`

- [x] 2. 创建插件目录结构

  **What**: 创建 `plugins/emote-plugin/`、`data/` 子目录、`config.yaml` 模板
  **Category**: `quick` | **Parallel**: Wave 1 (with 1,3-5)

  **QA**:
  ```
  ls plugins/emote-plugin/ → 目录存在
  cat plugins/emote-plugin/config.yaml → YAML正确
  ```
  **Evidence**: task-2-structure.txt, task-2-config.txt
  **Commit**: `feat(emote-plugin): create plugin structure`

- [x] 3. 插件框架 + 配置加载

  **What**: 实现 `Info/OnMessage/OnResponse/Shutdown`，`ListTools/ExecuteTool`，从 `EZYAPPER_PLUGIN_CONFIG` 加载配置
  **Category**: `unspecified-high` | **Parallel**: Wave 1 (with 1,2,4,5)
  **Refs**: `examples/plugins/antispam-go/main.go`

  **QA**:
  ```
  go test -run TestInfo → 返回正确元数据
  go test -run TestListTools → 返回3个工具
  ```
  **Evidence**: task-3-info.txt, task-3-tools.txt
  **Commit**: `feat(emote-plugin): implement framework`

- [x] 4. 存储层

  **What**: 原子写入（temp→rename），metadata.json 读写，SHA256去重，黑白名单检查，频率限制
  **Category**: `unspecified-high` | **Parallel**: Wave 1 (with 1-3,5)

  **QA**:
  ```
  go test -run TestAtomicWrite → 正确写入，无残留
  go test -run TestConcurrentWrite → 50并发，数据完整
  go test -run TestDedup → SHA256相同，拒绝
  go test -run TestBlacklist → 黑名单ID，拒绝
  ```
  **Evidence**: task-4-atomic.txt, task-4-concurrent.txt, task-4-dedup.txt, task-4-blacklist.txt
  **Commit**: `feat(emote-plugin): implement storage layer`

- [x] 5. OnMessage 自动偷取（核心）

  **What**: `OnMessage` 中遍历 `msg.AttachmentURLs`，对每个图片URL执行：黑白名单检查 → 下载 → SHA256去重 → Vision分析（判断是否表情包，生成name/tags/description） → 保存。Vision判定非表情包则跳过，不干扰正常消息。
  
  **Category**: `deep` | **Parallel**: Wave 1 (with 1-4)
  **Refs**: `examples/plugins/openai-tts-go/main.go`（HTTP下载），`internal/ai/client.go:703`（DownloadImage）

  **QA**:
  ```
  go test -run TestOnMessage_Steal → Mock图片+MockVision=is_emote → 文件保存，metadata更新
  go test -run TestOnMessage_NotEmote → MockVision=not_emote → 跳过，不保存
  go test -run TestOnMessage_Dedup → 相同图片两次 → 第二次跳过
  go test -run TestOnMessage_Blacklist → 黑名单频道 → 跳过
  go test -run TestOnMessage_RateLimit → 连续3次 → 第3次被限
  ```
  **Evidence**: task-5-steal.txt, task-5-not.txt, task-5-dedup.txt, task-5-blacklist.txt, task-5-ratelimit.txt
  **Commit**: `feat(emote-plugin): implement OnMessage auto-steal`

- [x] 6. list_emotes 工具
- [x] 7. search_emote 工具
- [x] 8. get_emote 工具

  **What**: 按ID或名称获取表情详情，检查文件存在性
  **Category**: `unspecified-high` | **Parallel**: Wave 2 (with 6,7,9-11)
  **Blocked by**: 3,4

  **QA**:
  ```
  go test -run TestGetEmote → 返回详情；文件缺失返回错误
  ```
  **Evidence**: task-8-get.txt
  **Commit**: `feat(emote-plugin): implement get_emote`

- [ ] 9. 配置验证 + 错误处理

  **What**: 必需项验证，默认值，优雅错误恢复
  **Category**: `unspecified-high` | **Parallel**: Wave 2 (with 6-8,10,11)
  **Blocked by**: 3

  **QA**:
  ```
  go test -run TestConfig → 有效通过，无效返回清晰错误
  ```
  **Evidence**: task-9-config.txt
  **Commit**: `feat(emote-plugin): config validation`

- [ ] 10. 单元测试

  **What**: 覆盖3个工具 + 存储 + OnMessage + 配置，覆盖率 > 80%
  **Category**: `unspecified-high` | **Parallel**: Wave 2 (with 6-9,11)
  **Blocked by**: 4-9

  **QA**:
  ```
  go test -cover -v → 全部PASS，覆盖率 > 80%
  ```
  **Evidence**: task-10-coverage.txt
  **Commit**: `test(emote-plugin): unit tests`

- [ ] 11. 集成测试 + 文档

  **What**: 端到端集成测试，README
  **Category**: `unspecified-high` | **Parallel**: Wave 2 (with 6-10)
  **Blocked by**: 4-9

  **QA**:
  ```
  go test -run TestIntegration → 全流程通过
  cat README.md → 含安装/配置/使用
  ```
  **Evidence**: task-11-integration.txt, task-11-docs.txt
  **Commit**: `test(emote-plugin): integration tests + docs`

---

## Final Verification Wave

- [ ] F1. **Plan Compliance Audit** — `oracle`
- [ ] F2. **Code Quality Review** — `unspecified-high`
- [ ] F3. **Real Manual QA** — `unspecified-high`
- [ ] F4. **Scope Fidelity Check** — `deep`

---

## Commit Strategy

| Task | Message | Files |
|------|---------|-------|
| 1 | `feat(plugin): add AttachmentURLs to DiscordMessage` | `internal/plugin/plugin.go` |
| 2 | `feat(emote-plugin): create plugin structure` | `plugins/emote-plugin/` |
| 3 | `feat(emote-plugin): framework` | `main.go` |
| 4 | `feat(emote-plugin): storage layer` | `storage.go` |
| 5 | `feat(emote-plugin): OnMessage auto-steal` | `main.go`, `vision.go` |
| 6 | `feat(emote-plugin): list_emotes` | `tools.go` |
| 7 | `feat(emote-plugin): search_emote` | `tools.go` |
| 8 | `feat(emote-plugin): get_emote` | `tools.go` |
| 9 | `feat(emote-plugin): config validation` | `config.go` |
| 10 | `test(emote-plugin): unit tests` | `*_test.go` |
| 11 | `test(emote-plugin): integration tests + docs` | `*_test.go`, `README.md` |

---

## Success Criteria

```bash
go test ./internal/plugin/...        # PASS（向后兼容）
go build ./plugins/emote-plugin/...  # SUCCESS
go test ./plugins/emote-plugin/...   # PASS, coverage > 80%
```

### Final Checklist
- [ ] DiscordMessage 一个字段 + FromDiscordgo 3行
- [ ] OnMessage 自动偷取（Vision决定，主LLM零参与）
- [ ] SHA256 去重 + 黑白名单 + 频率限制
- [ ] 3个LLM查询工具
- [ ] 原子写入
- [ ] 全部测试通过
