***

# 🤖 面向 AI Agent 的 CLI 工具设计规范 (Agent-Native CLI Spec)

**版本:** V2.2 (实践修订版)
**目标:** 彻底消除人类终端交互带来的解释歧义与"大模型幻觉"，构建 **100% 机器可读 (Machine-readable)**、行为确定、具备自我描述与生命周期自治能力的 AI Agent 基础设施生态。

> **V2.2 修订说明：** 基于 web-clip-helper（Python + Typer CLI）的 M004–M006 里程碑经验，对 V2.1 进行了补充修订。新增：2.4 静默模式、2.5 幂等响应模式、5.5.2 跨平台路径适配、6.4 数据迁移策略、附录 B 陷阱 8–10。修订以 `> 💡 实践注记：` blockquote 标注。

---

## 第一章 核心设计基本法 (Core Philosophy)

1. **绝对结构化通信 (JSONL-Only)**
   - 抛弃一切人类终端审美。标准输出 (stdout) 和标准错误 (stderr) 必须强制且唯一使用 **JSON Lines (JSONL)** 格式。
   - 严禁任何颜色转义符 (ANSI Codes)、进度条动画 (如 tqdm) 或 ASCII 表格。
2. **纯无头与零交互 (Headless Execution)**
   - 严禁出现 `input()`, `[y/N]` 或交互式密码输入。
   - 所有必要参数必须通过 CLI args、环境变量或配置文件一次性传入。若缺少参数，直接抛出参数缺失错误并退出，由 Agent 重构命令。
3. **被动响应与绝不擅权 (Explicit & Passive)**
   - 工具绝不擅作主张。禁止"静默无限重试"、"私自上报遥测"或隐式修复逻辑。工具只反映客观现实，控制流、重试链路与业务决策权必须**全权交还给 AI Agent 编排**。
4. **脱敏红线 (Redaction By Default)**
   - 任何收集环境快照、配置树、崩溃 Dump 的指令，必须在 CLI 内部实现自动脱敏。所有包含密钥、Token、密码的字段值应被硬编码替换为 `[REDACTED]`。

> 💡 实践注记：wr 的 `Config.Redacted()` 方法提供了一份安全副本，所有 JSONL 输出和日志均只接触脱敏后的副本。密钥保留前 4 字符用于"哪个 Key 缺失"的故障排查，其余替换为 `****`。这一模式建议在规范级别推广为 mandatory。

---

## 第二章 机器通信协议规范 (Protocol)

### 2.1 统一 JSONL 流出结构

标准输出的内容必须被包裹在具有固定语义和生命周期属性的顶层 JSON 对象中。

#### 2.1.1 Envelope 定义

```json
{
  "version": "1.0",
  "tool": "your-cli-name",
  "type": "result | error | warning | progress | schema | dict | diagnostics",
  "timestamp": "2026-05-03T10:00:00Z",

  // type="result" 时 (执行终态)
  "data": { "key": "value" },

  // type="error" 时 (错误中断)
  "error_code": "NETWORK_TIMEOUT",
  "message": "精确的故障原因描述",

  // type="progress" 时 (过程同步，支持通过 --quiet 屏蔽)
  "percent": 50,
  "message": "处理分片 3/5 ..."
}
```

#### 2.1.2 字段互斥规则（V2.1 新增）

`data`、`error_code`、`percent` 三个字段**严格互斥**，由 `type` 决定哪个字段出现：

| type | 允许字段 | 禁止字段 |
|------|----------|----------|
| `result` | `data` (必填), `message` (可选) | `error_code`, `percent` |
| `error` | `error_code` (必填), `message` (必填) | `data`, `percent` |
| `warning` | `message` (必填) | `data`, `error_code`, `percent` |
| `progress` | `percent` (必填), `message` (可选) | `data`, `error_code` |

**实现建议：** 使用 `omitempty` JSON tag 确保零值字段不出现在输出中。在测试中添加 field-separation 断言（如 wr 的 `TestEnvelopeFieldSeparation`）防止回归。

> 💡 实践注记：wr 的实现中，`Envelope` 结构体的 `Data`、`ErrorCode`、`Percent` 三个字段均标记 `omitempty`。`SuccessEnvelope()` 只填 `Data`，`ErrorEnvelope()` 只填 `ErrorCode` + `Message`，`ProgressEnvelope()` 只填 `Percent` + `Message`。这种"构造函数隔离"模式比在运行时做校验更可靠——类型系统本身就是文档。

#### 2.1.3 ErrorCode 必须为 string 类型

`error_code` 字段**必须使用大写蛇形字符串**（如 `"NETWORK_TIMEOUT"`、`"INVALID_BODY"`），不得使用整数错误码。

**理由：**
- 字符串 error_code 具有自描述性，Agent 无需额外查表即可理解错误类别。
- 在 CLI 进程退出码、HTTP 响应、JSONL 日志之间传递时保持一致性，不需要维护 int→string 的映射层。
- 便于 `agent errors` 命令直接输出排障字典。

> 💡 实践注记：wr 的 `exitcode.FromErrorCode()` 函数将 daemon 端的 string error_code 映射为 CLI 进程的 int 退出码。daemon 全链路只使用 string，只在 CLI 侧做一次转换。如果反过来用 int error_code，daemon 和 CLI 之间需要维护两套枚举，增加不一致风险。

### 2.2 语义化退出码 (Semantic Exit Codes)

退出码旨在让 Agent 在不进行 JSON 反序列化的情况下完成快分支决策：

| 退出码 | 语义 | Agent 推荐策略 |
|--------|------|----------------|
| `0` | 成功 | 继续下个节点 |
| `1` | 致命/未知错误 | 调取 dump 命令提报 |
| `2` | 参数/输入无效 | 修正 prompt 参数重试 |
| `3` | 依赖/资源缺失（daemon 不可达） | 检查 daemon 状态并启动 |
| `4` | 网络/第三方熔断 | sleep 后退避重试 |
| `5` | 并发资源互斥 | 排队等待 |

> 💡 实践注记：wr 实际实现中，退出码 3 的语义从泛化的"资源缺失"收窄为"daemon 不可达"。原因是 CLI→HTTP daemon 架构下，最常见的 exit 3 场景就是 daemon 没启动或状态文件过期。建议规范允许工具在保留退出码大类的前提下，细化语义描述。

### 2.3 错误码分层体系（V2.1 新增）

error_code 应按关注点分离原则分为三层，每层使用不同命名前缀：

| 层级 | 前缀 | 示例 | 说明 |
|------|------|------|------|
| **结构层** | 无特定前缀 | `invalid_body`, `invalid_type`, `method_not_allowed` | 请求格式/协议层面的错误 |
| **语义层** | 与业务域相关 | `record_not_found`, `already_completed`, `pushover_not_configured` | 业务逻辑层面的错误 |
| **基础设施层** | `daemon_` / `llm_` / `push_` | `daemon_not_running`, `llm_error`, `push_error` | 外部依赖/运行环境错误 |

**规则：**
- Handler 在最内层产生语义错误码（如 `record_not_found`）。
- 中间件/客户端包装器添加基础设施错误码（如 `daemon_not_running`）。
- 禁止跨层混用（不要在 handler 里返回 `daemon_not_running`）。

> 💡 实践注记：wr 的 handler 层返回 `storage_error`、`record_not_found` 等语义码，而 client 包包装为 `daemon_not_running`。两层职责清晰，且 `exitcode.FromErrorCode()` 可以根据层级精确映射退出码。

### 2.4 静默模式（Quiet Mode）

工具**必须**提供 `--quiet` / `-q` 全局标志，允许 Agent 在不需要过程同步时屏蔽噪声输出。

**规则：**
- `--quiet` 开启后，只输出 `type=result` 和 `type=error` 行，屏蔽 `type=progress` 和 `type=warning`。
- 静默过滤**必须在 JSONL 发射的单一瓶颈点实现**（如 `jsonl_emit()` 函数），而非在各调用点逐个判断。
- `type=result` 和 `type=error` 永远不可屏蔽——即使在 quiet 模式下也必须输出。

```json
// 正常模式：5 行输出
{"type":"progress","percent":20,"message":"fetching page..."}
{"type":"progress","percent":60,"message":"extracting content..."}
{"type":"warning","message":"image 3/10 failed, skipping"}
{"type":"progress","percent":90,"message":"generating metadata..."}
{"type":"result","data":{"id":42,"title":"..."}}

// --quiet 模式：1 行输出
{"type":"result","data":{"id":42,"title":"..."}}
```

**理由：** Agent 的上下文窗口有限。progress 行在交互式终端中有用，但对 Agent 来说是纯噪声。单一瓶颈点过滤确保新增的输出路径自动遵守 quiet 规则，不需要逐调用点添加条件判断。

> 💡 实践注记：web-clip-helper 的 `--quiet` 标志在 `jsonl_emit()` 函数中统一检查 `_quiet_mode` 模块级状态。所有 progress/warning 输出路径（包括管线中的抓取进度、LLM 调用提示、图片下载警告）均通过此函数发出，因此自动被 quiet 模式覆盖。测试时需注意：模块级状态（如 `_quiet_mode`）会跨测试用例泄漏，必须使用 `autouse` fixture 在每个测试前重置。

### 2.5 幂等操作的标准响应模式

当 `agent schema` 标注某命令为 `"is_idempotent": true` 时，重复调用该命令应返回幂等响应而非创建重复资源。

**幂等响应必须包含：**

```json
{
  "type": "result",
  "data": {
    "duplicate": true,
    "existing_id": "42",
    "id": "42",
    "title": "已有记录的标题",
    "...": "完整记录详情"
  }
}
```

**规则：**
- `duplicate` 字段（boolean）为幂等检测的标志——`true` 表示本次操作匹配到已有资源。
- `existing_id` 字段指向已有资源的 ID。
- 返回的 `data` 中应包含已有资源的完整信息，以便 Agent 无需额外查询。
- 幂等检测的匹配键**必须在 `agent schema` 的命令描述中明确声明**（如 "幂等键: URL"）。
- 当幂等检测失败（如 DB 查询异常）时，工具应**防御性地 fallthrough 到正常执行路径**，而不是阻断操作。

**理由：** Agent 的编排逻辑经常包含重试（网络超时后重发同一请求）。如果不返回幂等标记，Agent 无法区分"创建了新记录"和"返回了已有记录"，可能导致状态推断错误。防御性 fallthrough 确保幂等检测的故障不阻塞正常操作。

> 💡 实践注记：web-clip-helper 的 `clip` 命令使用 URL 作为幂等键。`normalize_url()` 在 save 和 query 两侧统一应用（http→https + trailing slash 去除），确保 DB 中始终存储规范 URL。当 `find_by_url()` 查询失败（如 SQLite 错误）时，代码 fallthrough 到正常 clip 流程——宁可多创建一条记录，也不因幂等检测故障而阻断用户操作。

---

## 第三章 全局通用保留命令空间 (Reserved Meta-Commands)

**设计契约：** 为了让 Agent 能够"一套逻辑通吃所有工具"，凡遵守此规范的 CLI，必须将 `agent` 注册为一级保留命令，并强行实现以下 6 大类、16 个"元管理动作"。**业务命令严禁占用该空间。**

### 3.1 发现与语义嗅探模块 (Discovery)
* `[CLI] agent info`：输出工具当前版本、最后更新日期、文档指引。
* `[CLI] agent schema`：输出全量 JSON Schema，包含每一个业务命令的必填项、类型、描述，以及是否允许盲冲的 `"is_idempotent": true/false` 标识。
* `[CLI] agent errors`：输出工具独有的自定义排错字典列表（`error_code` 到排障指引的映射）。

### 3.2 状态监测与生命周期模块 (Health)
* `[CLI] agent doctor`：执行系统权限、网络、守护进程健康度体检，返回确定的 Pass/Fail 状态。
* `[CLI] agent update check`：只读不写，获取是否有可用的二进制远端新版本，并携带变化说明（Breaking Changes）。
* `[CLI] agent update apply`：触发本程序的原地版本替换与升级校验。

### 3.3 凭据与配置接管模块 (Auth & Config)
* `[CLI] agent auth status`：检查凭证有效性与配额。绝不能输出明文 Token，仅返回 boolean 和过期时间。
* `[CLI] agent config list --redact`：盘点所有挂载的配置设定（强制脱敏）。
* `[CLI] agent config set <key> <value>`：Agent 运行时热修改特定环境数值（如延长超时期）。

> 💡 实践注记：wr 的 `config set` 使用路径白名单（`ValidConfigPaths()`）防止拼写错误静默创建无效键。建议规范要求所有 `config set` 实现路径校验，而非盲目写入。

### 3.4 痕迹清理与系统资源模块 (Resource)
* `[CLI] agent cache clean`：清理上一次任务遗留的缓存脏文件或长期累积的 tmp 文件。
* `[CLI] agent daemon status|start|stop`：（如果存在守护进程）纯无头级别的进程启停开关。

### 3.5 灾难取证模块 (Diagnostics)
* `[CLI] agent debug last-crash`：致命退出后（Exit Code 1），提取包含 Call Stack（死机堆栈）与调用参数字典的全真上下文。
* `[CLI] agent debug env --redact`：提取 OS 硬件环境、核心依赖情况组成的 Issue 快照。

### 3.6 反馈溯源模块 (Feedback)
* `[CLI] agent feature record --name <n> --desc <d>`：当业务逻辑受限（抛出 NotImplemented 时），记录缺失的能力诉求至沙箱。
* `[CLI] agent feature list`：统一导出过往累计的需求清单。
* `[CLI] agent metrics trace --id <job_id>`：按单一 Trace ID 审计该任务消耗的时间、API 成本统计。

---

## 第四章 大模型防爆与高可用防御 (Robustness Definitions)

### 4.1 体积控制与指针优先 (Payload Serialization Base)
大模型的上下文容量有限。任何输出预期超 100 行的操作（如历史记录导出），必须强制接管进行 `--limit` 分页，或在 JSONL 中返回**文件指针** (`{"file_uri": "/tmp/a.json"}`) 而不是原始巨量层级数据。

### 4.2 并发踩踏锁 (Concurrency Locks)

面对多个 Agent 在同一时间并行调度，如涉及底层配置写操作或自升级，必须添加**文件锁**。并发抢占失败时一律返回明确码：`{"error_code": "RESOURCE_LOCKED", "retry_after": 5}`。

#### 4.2.1 锁粒度（V2.1 新增）

锁的粒度取决于架构：

| 场景 | 锁粒度 | 理由 |
|------|--------|------|
| 单进程 daemon + 多 Agent 并发 | **无进程内锁** | HTTP handler 在同一进程内串行化（Go 的 mutex） |
| 多 daemon 实例 / 多进程并发写入 | **文件锁** | 保护共享状态文件 |
| Agent 并发操作同一 daemon | **无需锁** | daemon 是单入口，请求天然串行 |

> 💡 实践注记：wr 采用 CLI→HTTP daemon 架构。daemon 是单进程（`sync.Mutex` 保护写操作，如 `Storage.mu`），不需要跨进程文件锁。退出码 5 的锁冲突在 wr 中保留为预留接口，实际触发场景是"多个 daemon 实例竞争同一端口"。**关键教训：不要在单进程内部过度加锁——Go 的 mutex 已经足够。文件锁的适用场景是多 agent 直接写共享文件（如状态文件），不是 HTTP 请求的串行化。**

### 4.3 优雅遗言与死亡捕获 (Graceful Shutdown)
CLI 进程需捕获来自外围或编排系统的强杀信号（`SIGTERM` / `SIGINT`）。退出前的几十毫秒内，务必将当前数据上下文刷入本地 `.last-crash` 文件记录，状态标记为 `AGENT_ABORTED`，绝不凭空消失。

### 4.4 Daemon 进程的日志约束（V2.1 新增）

当 CLI 采用 **CLI→HTTP daemon→storage** 分离架构时，daemon 进程的日志必须遵守以下约束：

1. **daemon 的 `log.Printf` 必须写文件**（如 `~/.app-name/daemon.log`），不得输出到 stdout。
2. **只有 HTTP 响应体走 JSONL 协议**。daemon 的 stderr 可以在启动阶段使用，但稳态运行后应全部重定向到日志文件。
3. **daemon 必须提供日志轮转或截断策略**，防止日志文件无限增长。

> 💡 实践注记：wr 的 daemon 通过 `sandbox.OpenDaemonLog()` 将 `log.Default()` 重定向到 `~/.work-report/daemon.log`。daemon 内部的所有 `log.Printf` 走这个文件句柄，只有 HTTP 响应体走 JSONL。如果 daemon 的调试日志污染了 stdout，Agent 解析 JSONL 时会收到格式错误的数据——这是早期开发中踩过的坑。

### 4.5 Panic 捕获的双重场景（V2.1 新增）

Panic 捕获需要区分两个不同的执行环境：

| 场景 | 捕获位置 | 恢复策略 |
|------|----------|----------|
| **CLI panic** | `cmd.Execute()` 的 `defer recover()` | 输出 `{"type":"error","error_code":"FATAL_CRASH","message":"panic: ..."}` JSONL → `os.Exit(1)` |
| **daemon panic** | HTTP handler chain 的 `panicRecoveryMiddleware` | 输出同样的 `FATAL_CRASH` JSONL 响应体 + 写 daemon 日志 + HTTP 200（保持 JSONL 通道不被破坏） |

**关键约束：** daemon 的 panic 恢复中间件必须返回 HTTP 200 而非 500。原因是 JSONL 协议的错误信息已经编码在响应体中，HTTP 状态码用于传输层判断（Agent 首先看 HTTP 是否可达，再解析 JSONL 判断业务结果）。混合使用会导致 Agent 的错误分类逻辑复杂化。

> 💡 实践注记：wr 的实现中，`cmd.Execute()` 用 `defer func() { recover(); jsonl.ErrorWithCode("FATAL_CRASH", ...); os.Exit(1) }()` 兜底 CLI panic。daemon 侧用 `panicRecoveryMiddleware` 兜底 handler panic，返回 HTTP 200 + JSONL error envelope。两层互不干扰。测试时需要在 CLI 层和 daemon 层分别验证 panic 恢复路径。

---

## 第五章 工程实施与质量保障 (Engineering & QA)

### 5.1 防幻觉重灾：演习模式规范 (`--dry-run`)

所有涉及数据插入、修改、删除的关键动作都必须支持 `--dry-run`。Agent 附加此参数调用时，工具绝不执行真实 IO，只返回结构化的模拟变动范围（Execution Plan），供后续审计。

#### 5.1.1 dry-run 实现位置（V2.1 新增）

`--dry-run` **应在 daemon 侧实现**，而非 CLI 侧。

**理由：**
- dry-run 的价值在于精确预览执行结果，包括 LLM 分类结果、存储路径、ShortID 生成等。
- 如果只在 CLI 侧短路（不发送请求到 daemon），Agent 无法预览 LLM 会如何分类输入。
- daemon 侧实现允许 dry-run 穿过完整的校验链（参数验证、类型检查、schema 校验），只跳过最终的 IO 写入。

> 💡 实践注记：wr 选择在 daemon handler 层实现 dry-run（虽然当前版本尚未实现），原因是 `wr add` 可能触发 LLM 分类。如果 CLI 侧短路，Agent 就无法知道"这条自然语言会被分类成什么类型"。dry-run 应该执行到"准备写入"的前一步，返回完整的 Execution Plan。

### 5.2 跨域追踪：Trace ID Context

`AGENT_TRACE_ID` **必须仅通过环境变量传递**，不得作为全局 CLI flag。

**理由：**
- 全局 flag 侵入 flag 命名空间，可能与业务参数冲突。
- 环境变量是进程级的天然透传机制，Agent 编排器（如 pi、GPT-Shell）已在进程启动时设置。
- 环境变量不需要在每个子命令中重复声明。

工具的所有输出（正常、脱轨、Dump 文件）均需带上原有 ID，以实现在复杂数据流中"追踪一个 Agent 的连锁足迹"。

> 💡 实践注记：wr 当前未实现 trace ID，但架构已预留位置——环境变量 `AGENT_TRACE_ID` 可在 daemon 启动时注入，然后透传到所有 JSONL envelope 和日志行中。如果做成全局 flag（`--trace-id`），每个 cobra 子命令都需要 `PersistentFlags()`，增加了维护成本和冲突风险。

### 5.3 拒接手敲：SDK 与 Linter 强制保底

必须在技术栈基层实现轻量 SDK 层。**SDK 职责：** 全局捕获 Panic 转原生 JSONL，并通过代码反射动态自动生成 `agent schema`。

CI/CD 流水线发版前，必须运行 Linter 对其随机参数注入检测：只要发生因为人为粗心 `print("debug log")` 破坏了 JSONL 流完整性的情况，立即阻断合并发版。

#### 5.3.1 Schema 生成：声明式 vs 反射（V2.1 新增）

`agent schema` 的生成需要在两种策略间做出取舍：

| 策略 | 优点 | 缺点 |
|------|------|------|
| **声明式**（手写 JSON Schema） | 精确控制描述、示例、约束；可标注 `is_idempotent` | 维护成本高，容易与代码不同步 |
| **反射式**（从代码结构自动生成） | 始终与代码同步，零维护 | 拿不到字段描述（description）、拿不到 `is_idempotent` 等业务标注 |

**推荐策略：混合式**
- 从代码反射生成结构骨架（字段名、类型、必填性）。
- 通过 struct tag 或配套声明文件补充描述和业务标注。
- CI 中校验声明文件与代码的一致性。

> 💡 实践注记：Go 的 `encoding/json` 反射只能拿到字段名和类型，拿不到 `description` 或 `is_idempotent`。wr 的 `agent schema`（如果实现）需要配合一份 YAML/JSON 声明文件来补充这些元数据。纯粹依赖反射的 schema 生成对 Agent 来说价值有限——Agent 需要的是"这个字段应该传什么"，而不只是"这个字段是 string"。

### 5.4 隐性知识注入 (`AGENT_INSTRUCTION.md`)

在每个工具仓根目录下必须包含面向 LLM 的业务 Markdown 指南，陈述工具的业务边际、以及串联操作的最佳 SOP 工作流。

### 5.5 沙箱路径规范

#### 5.5.1 路径自定义与内部分区（V2.1 新增）

沙箱路径应**允许通过配置自定义**，但必须**强制内部分区**：

```
<base_dir>/            ← 可自定义（默认 ~/.app-name/）
├── locks/             ← 必须
├── crash_dumps/       ← 必须
├── cache/             ← 必须
└── daemon.log         ← 必须
```

**规则：**
- `base_dir` 允许通过环境变量或 `agent config set data_dir` 自定义。
- 但内部分区（`locks/`、`crash_dumps/`、`cache/`）的名称和存在性由工具强制保证。
- 工具启动时调用 `EnsureSandboxDirs(baseDir)` 创建所有必需子目录。

> 💡 实践注记：wr 的数据目录默认 `~/.work-report/`，可通过 `config.json` 的 `data_dir` 字段自定义。但沙箱子目录（`locks/`、`crash_dumps/`、`cache/`）的名称硬编码在 `sandbox` 包中，不允许自定义。这种"外柔内刚"的策略平衡了灵活性和可预测性。存量工具迁移时，只需修改 `base_dir`，不用改代码。

#### 5.5.2 跨平台路径适配（V2.2 新增）

不同操作系统的标准目录路径差异极大，工具**必须**使用平台感知的库自动解析路径：

| 操作系统 | 配置目录 | 数据目录 | 状态/缓存目录 |
|----------|----------|----------|---------------|
| Linux | `~/.config/<app>/` | `~/.local/share/<app>/` | `~/.local/state/<app>/` |
| macOS | `~/Library/Application Support/<app>/` | 同左 | 同左 |
| Windows | `%APPDATA%\<app>\` | `%LOCALAPPDATA%\<app>\` | `%LOCALAPPDATA%\<app>\` |

**规则：**
- **禁止硬编码路径**。使用平台感知库（Go: `os.UserConfigDir()`/`os.UserCacheDir()`；Python: `platformdirs`；Node: `env-paths`）自动解析。
- 如果不使用 XDG 标准路径（如 wr 选择 `~/.work-report/`），必须在文档中明确说明原因，并提供配置覆盖机制。
- 沙箱内部分区目录（`locks/`、`crash_dumps/`、`cache/`）的名称在不同平台保持一致——跨平台差异只影响 `base_dir`，不影响内部结构。

> 💡 实践注记：web-clip-helper 使用 Python 的 `platformdirs` 库（封装在 `paths.py` 单一真相源模块中）自动处理三平台差异。`paths.py` 提供统一的 `get_data_dir()`/`get_config_dir()`/`get_cache_dir()` 接口，CLI 层和业务层不直接引用任何路径常量。跨平台测试需在 Windows 和 Linux/macOS 上分别验证路径解析结果——仅靠 `t.TempDir()` 覆盖 `HOME` 无法覆盖 Windows 的 `%APPDATA%` 逻辑。

---

## 第六章 数据生命周期 (Data Lifecycle)

> 本章为 V2.1 新增。

Agent-Native CLI 工具管理的数据具有完整的生命周期：创建、读取、更新、导出、归档。规范要求工具对这些操作提供明确的、JSONL 化的接口。

### 6.1 数据导入 (Import)

#### 6.1.1 Fail-Fast 策略

导入操作必须遵循 **fail-fast** 原则：

1. 先做全量校验（schema 校验、必填字段、类型合法性），收集所有错误。
2. 如果存在任何校验错误，立即返回所有错误列表，**不写入任何记录**。
3. 校验全部通过后，才执行批量写入。

```json
// 校验失败示例
{
  "type": "error",
  "error_code": "import_validation_failed",
  "message": "3 validation errors",
  "data": {
    "errors": [
      {"line": 5, "field": "date", "reason": "invalid date format"},
      {"line": 12, "field": "type", "reason": "unknown type: 'incident'"},
      {"line": 18, "field": "title", "reason": "missing required field"}
    ],
    "total": 20,
    "valid": 17,
    "invalid": 3
  }
}
```

#### 6.1.2 导入源

导入源应支持以下格式（按优先级）：
1. JSONL 文件（每行一条记录）
2. JSON 数组文件
3. 标准输入管道（stdin）

### 6.2 数据导出 (Export)

#### 6.2.1 过滤导出

导出操作必须支持**过滤参数**，避免 Agent 获取不必要的数据：

- `--type`：按记录类型过滤
- `--from` / `--to`：按日期范围过滤
- `--status`：按状态过滤（active、completed、all）
- `--query`：按关键词搜索（title、description）
- `--limit` / `--offset`：分页控制

#### 6.2.2 输出格式

导出输出**必须为 JSONL 格式**（每行一条记录的完整 JSON），而非嵌套 JSON 数组。理由：
- JSONL 支持流式处理，Agent 可以逐行解析而不需要一次性加载整个数组。
- JSONL 天然兼容 `|` 管道和 `>` 重定向。

### 6.3 数据归档

已完成或已取消的记录应从活跃存储区移出，但仍可通过带 `--status completed` 或 `--status all` 的查询访问。

> 💡 实践注记：wr 的 `complete` 操作将记录从 `tasks/active/` 移动到 `tasks/completed/YYYY/MM/DD/`。`list` 命令默认只返回 active 记录，需要 `--status completed` 或 `--status all` 才能查询已完成记录。这种"默认隐藏、按需展开"的策略减少了 Agent 日常操作的数据噪声。

### 6.4 数据迁移策略（V2.2 新增）

存量工具在升级到规范要求的 XDG 沙箱目录布局时，需要将历史数据从旧路径迁移到新路径。迁移必须**零数据丢失**。

#### 6.4.1 复制 + 标记模式

迁移**必须使用复制（copy）而非移动（move）**，配合标记文件：

```
旧目录：~/.app-name/
├── config.yaml
├── clips.db
├── clips/
└── .migrated          ← 迁移完成后创建的标记文件

新目录（XDG 标准）：
~/.config/app-name/config.yaml
~/.local/share/app-name/clips.db
~/.local/share/app-name/clips/
```

**规则：**
1. 迁移前检查标记文件（`.migrated`）。若已存在则跳过迁移。
2. 复制所有数据文件到新路径。旧路径数据保持不变。
3. 复制成功后，在旧目录创建 `.migrated` 标记文件。
4. 后续所有操作只读写新路径。
5. **不删除旧目录**——保留旧数据作为回退保障。

**理由：** 移动操作在失败时不可恢复（如磁盘满、权限不足）。复制确保旧数据始终存在，即使新路径出问题也能回退。标记文件防止重复迁移。

#### 6.4.2 迁移失败处理

- 任何文件复制失败 → 停止迁移，使用旧路径继续运行，输出 `type=warning` 提示迁移未完成。
- 标记文件存在但新路径数据不完整 → 删除标记文件，下次启动时重新尝试。
- **永远不要因为迁移失败而阻止工具正常运行。**

> 💡 实践注记：web-clip-helper 在 `paths.py` 的 `ensure_dirs()` 中自动触发迁移。迁移覆盖 config.yaml、clips.db、clips/ 目录和 reports/ 目录。如果目标文件已存在（如用户手动复制过），则跳过该文件（幂等性）。迁移后工具对旧目录只做读检查（标记文件是否存在），不再写入。

---

## 附录 A：标准 Agent System Prompt (适配器调用逻辑示例)

为了最大化发挥本规范价值，请在唤起大语言模型 Agent 时植入以下框架逻辑：

> **🌍 面向 AI Agent 的 CLI 操作铁律：**
> 1. 新接触本系统任意命令前，严禁产生"常识性幻觉假设"，必须首选执行 `agent schema` 获取正确参数和动作边界。
> 2. 对于 `is_idempotent: false` 及高危数据操作，强制先附带 `--dry-run`；对于资源密集型操作前先通过 `agent doctor` 体检。
> 3. 若目标由于网络/环境抛出可修复错误代码 (例如 Exit 3 或 4)，根据 `agent errors` 诊断建议重试或修正。**Exit 3 通常表示 daemon 未启动——先执行 `agent daemon start`。**
> 4. 若目标由于底层缺陷抛出不可逆代码 (Exit 1)，即刻调用 `agent debug last-crash` 加 `agent debug env --redact` 调取线索，并停止当前流程，输出完整 Bug 报告。
> 5. 系统能力无法直接完成推演的动作，不要虚构调用，立即使用 `agent feature record` 归档此功能痛点请求。
> 6. 导出操作优先使用过滤参数（`--type`、`--from`、`--to`、`--query`）缩小数据范围，避免消耗过多上下文窗口。
> 7. 操作完成后检查 JSONL 响应中的 `type` 字段——`result` 表示成功，`error` 表示失败，忽略其他字段。**不要假设字段是否存在——遵循 2.1.2 的互斥规则。**

---

## 附录 B：常见实现陷阱 (V2.1 新增)

以下是 wr 项目开发中实际遇到的问题，按严重程度排序：

### 陷阱 1：daemon 日志污染 JSONL 通道

**症状：** Agent 报告"收到的 JSONL 格式错误"。
**原因：** daemon 的 `log.Printf` 默认输出到 stdout，与 JSONL 响应体混在一起。
**修复：** daemon 启动时将 `log.Default()` 重定向到日志文件（`sandbox.OpenDaemonLog()`）。只有 HTTP 响应体走 JSONL。

### 陷阱 2：panic 恢复返回 HTTP 500

**症状：** Agent 的 HTTP 客户端将 500 视为"连接错误"而非"业务错误"，触发错误的退避策略。
**原因：** panic recovery middleware 使用 `w.WriteHeader(500)`。
**修复：** 始终返回 HTTP 200 + JSONL error envelope。让 JSONL 中的 `error_code` 携带所有业务语义，HTTP 状态码只用于传输层判断。

### 陷阱 3：Windows 时钟分辨率导致 ShortID 碰撞

**症状：** 批量导入时出现 ShortID 重复。
**原因：** Windows 的 `time.Now()` 分辨率约 100ns-1ms，同一毫秒内创建的记录可能生成相同的 SHA-256 ShortID。
**修复：** 使用单调递增计数器（monotonic sequence counter）作为 hash 输入的一部分（如 wr 的 `ShortIDFromTimestampAndSeq(t, seq)`）。

### 陷阱 4：测试依赖外部文件系统路径

**症状：** CI/worktree 环境下测试失败，因为找不到 fixture 文件。
**原因：** 测试使用 `testdata/` 目录下的外部 JSON 文件作为 fixture。
**修复：** 使用**内联 JSON 字符串**作为测试 fixture（如 `` `{"type":"task","title":"test"}` ``），避免文件系统依赖。`t.TempDir()` 用于需要真实目录的测试场景（如 storage CRUD），但 fixture 数据应内联。

### 陷阱 5：config set 静默创建无效键

**症状：** Agent 执行 `config set pushover.api_key sk-xxx`，但实际创建了一个拼写错误的键（如 `pushver.api_key`），后续启动时静默忽略。
**原因：** `config set` 直接写入任意 key，不做路径校验。
**修复：** 维护一个 `ValidConfigPaths()` 白名单，拒绝不在白名单中的路径。

### 陷阱 6：agent schema 反射丢失业务语义

**症状：** Agent 查阅 `agent schema` 后仍然不知道某个字段应该传什么值。
**原因：** 纯反射生成的 schema 只有字段名和类型，没有 description 和示例。
**修复：** 采用混合策略——反射生成骨架，手写补充描述和 `is_idempotent` 标注。

### 陷阱 7：错误码不分区导致 Agent 无法分类处理

**症状：** Agent 收到 `storage_error` 后无法区分"文件不存在"和"磁盘满"，只能一律重试。
**原因：** 所有错误都归为同一个 error_code。
**修复：** 遵循 2.3 节的三层错误码体系，让 Agent 能根据错误码精确决策（`record_not_found` → 修正 ID 重试，`disk_full` → 中断报告）。

### 陷阱 8：退出码映射与规范定义不一致

**症状：** Agent 的退避策略触发错误——例如参数无效（应 exit 2）时 Agent 看到 exit 1，误判为"致命错误"而放弃重试。
**原因：** 实现中的退出码映射与规范定义的语义方案不一致。常见偏离：将 `INPUT_INVALID` 归到 exit 1（致命）而非 exit 2（参数无效），或将 `NOT_FOUND` 归到 exit 2（参数）而非 exit 3（资源缺失）。
**修复：** 在每个 slice/模块的开发边界上对照规范验证 EXIT_CODE_MAP，而不是等到里程碑末尾才验证。在测试中为每个 error_code 建立明确的 `assert exit_code == expected` 断言，并确保 expected 值来源于规范定义的单一真相源。

### 陷阱 9：Python/Node 生态未做 stdout 劫持

**症状：** Agent 报告"收到的 JSONL 格式错误"——stdout 中混入了 ANSI 颜色码、进度条或调试日志。
**原因：** Python 的 Click/Typer 框架会在参数验证失败时将 Rich 格式文本直接写入 stdout；LLM SDK 的 debug 输出、第三方库的 `print()` 调用也会污染 JSONL 流。Go 生态的第三方库通常不会随意写 stdout，但 Python/Node 生态中这是常态。
**修复：** Python/Node 工具**必须**在启动第 1 毫秒劫持 `sys.stdout`/`sys.stderr` 到内存缓冲，所有输出通过 SDK 的唯一发射口（如 `jsonl_emit()`）写入真实 stdout。实现时注意：Python 的 `_FakeStream` 应使用组合模式而非继承 `io.TextIOBase`，因为 `TextIOBase` 的属性（如 `buffer`、`encoding`）可能与虚假对象冲突。Go 工具可以不做劫持（Go 生态的库不会随意 print），但 Python/Node **不做劫持等于违反规范**。

### 陷阱 10：非 JSONL 的 print() 绕过测试（AST Linter 缺失）

**症状：** 开发者在代码中添加 `print("debug info")` 后通过手工测试，但 CI 中没有自动检测，导致非 JSONL 输出泄漏到 Agent 消费流。
**原因：** 规范要求"必须运行 Linter 检测 print() 调用"，但没有给出具体实现方案。手工 code review 不可靠。
**修复：** 实现 AST（抽象语法树）级别的静态分析 Linter，检测所有不通过 SDK 发射口的 `print()` 调用。Linter 应：
  1. 解析源文件为 AST。
  2. 查找所有 `print()` / `fmt.Println()` / `console.log()` 调用。
  3. 排除已知安全路径（如 SDK 内部的 jsonl_emit 函数、测试文件）。
  4. 对违规调用报告文件名和行号。
  5. 在 CI 中作为发版阻断条件运行。

> 💡 实践注记：web-clip-helper 的 `scripts/check_jsonl_purity.py` 使用 Python `ast` 模块遍历 `src/web_clip_helper/` 目录下的所有 `.py` 文件，查找所有 `print()` 调用，排除测试目录和 `output.py` 中的 `jsonl_emit` 函数本身。22 个 Linter 测试覆盖了各种边界情况（f-string print、多参数 print、注释中的 print 等）。

---

## 附录 C：术语表

| 术语 | 定义 |
|------|------|
| **JSONL** | JSON Lines，每行一个独立 JSON 对象的文本格式 |
| **Envelope** | JSONL 输出的顶层包装对象，包含 version/tool/type/timestamp 等元数据 |
| **Exit Code** | CLI 进程退出时的整数状态码，供 Agent 做快分支决策 |
| **error_code** | JSONL Envelope 中的字符串错误标识，用于精确分类业务错误 |
| **Short ID** | 从时间戳或文件名派生的短标识符，用于 CLI 参数引用 |
| **Fail-Fast** | 校验阶段发现任何错误立即中止，不执行部分写入 |
| **Daemon** | 长期运行的后台进程，通过 HTTP API 接收 CLI 命令 |
| **dry-run** | 演习模式，执行完整校验链但不执行实际 IO 写入 |
