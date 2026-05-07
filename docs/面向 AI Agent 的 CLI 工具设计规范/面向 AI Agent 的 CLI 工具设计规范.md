***

# 🤖 面向 AI Agent 的 CLI 工具设计规范 (Agent-Native CLI Spec)

**版本:** V2.5 (Envelope kind 字段 + 测试隔离模式扩展 + on_help 钩子)
**目标:** 彻底消除人类终端交互带来的解释歧义与"大模型幻觉"，构建 **100% 机器可读 (Machine-readable)**、行为确定、具备自我描述与生命周期自治能力的 AI Agent 基础设施生态。

> **V2.5 修订说明：** 基于 M007 S01–S02 的 Python SDK 改进，新增 2.1.4（kind 字段说明：SDK 级可选字段、Go omitempty / Python None 省略语义）、5.7 扩展（Python reset_for_testing() 测试隔离模式、on_help 钩子说明）。修订 2.1.1（Envelope 定义增加 kind 可选字段）、2.1.2（字段互斥规则表增加 kind 列说明）。新增陷阱 13。
>
> **V2.4 修订说明：** 基于 M004 S01-S03 的 Go SDK 改进，新增 4.6（SDK Daemon 辅助模式：NewHTTPWriter 工厂函数、App.Registry() ErrorCode 映射）、5.5.3（跨平台原子写入：atomicReplace、Windows 跨卷 errno 17/18 回退）、5.6（结构化错误类型模式：WhitelistError/UnknownFieldError marker-method 惯例、errors.As 判断）、5.7（测试隔离模式：NewTestApp + WithTmpDir + AgentCommands 扩展钩子）。新增陷阱 11–12。
>
> **V2.3 修订说明：** 基于 wr（Go）和 web-clip-helper（Python）两个项目的实践经验，将路径策略从 XDG 多路径模式统一为**单目录模式**。理由：Agent-Native CLI 工具不是桌面应用，XDG 多路径带来的维护成本（路径管理代码膨胀、调试排查困难、测试隔离复杂、迁移逻辑沉重）远高于其收益。修订涉及：5.5.1（实践注记更新）、5.5.2（跨平台路径适配全面重写）、6.4（数据迁移策略从 XDG 迁移改为单目录内部迁移）。
>
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
  "message": "处理分片 3/5 ...",

  // SDK 级可选字段（V2.5 新增，见 2.1.4）
  "kind": "help | schema | ..."
}
```

#### 2.1.2 字段互斥规则（V2.1 新增）

`data`、`error_code`、`percent` 三个字段**严格互斥**，由 `type` 决定哪个字段出现：

| type | 允许字段 | 禁止字段 |
|------|----------|----------|
| `result` | `data` (必填), `message` (可选), `kind` (可选) | `error_code`, `percent` |
| `error` | `error_code` (必填), `message` (必填) | `data`, `percent`, `kind` |
| `warning` | `message` (必填) | `data`, `error_code`, `percent`, `kind` |
| `progress` | `percent` (必填), `message` (可选) | `data`, `error_code`, `kind` |

**`kind` 字段（V2.5 新增）：** `kind` 是 SDK 级可选字段，**不参与** `data`/`error_code`/`percent` 的互斥规则。它作为 `type=result` 的辅助语义标注（如 `kind=help` 表示帮助文本、`kind=schema` 表示 schema 内容），仅在调用方显式提供时出现。详见 §2.1.4。

**实现建议：** 使用 `omitempty` JSON tag 确保零值字段不出现在输出中。在测试中添加 field-separation 断言（如 wr 的 `TestEnvelopeFieldSeparation`）防止回归。

> 💡 实践注记：wr 的实现中，`Envelope` 结构体的 `Data`、`ErrorCode`、`Percent` 三个字段均标记 `omitempty`。`SuccessEnvelope()` 只填 `Data`，`ErrorEnvelope()` 只填 `ErrorCode` + `Message`，`ProgressEnvelope()` 只填 `Percent` + `Message`。这种"构造函数隔离"模式比在运行时做校验更可靠——类型系统本身就是文档。

#### 2.1.3 ErrorCode 必须为 string 类型

`error_code` 字段**必须使用大写蛇形字符串**（如 `"NETWORK_TIMEOUT"`、`"INVALID_BODY"`），不得使用整数错误码。

**理由：**
- 字符串 error_code 具有自描述性，Agent 无需额外查表即可理解错误类别。
- 在 CLI 进程退出码、HTTP 响应、JSONL 日志之间传递时保持一致性，不需要维护 int→string 的映射层。
- 便于 `agent errors` 命令直接输出排障字典。

> 💡 实践注记：wr 的 `exitcode.FromErrorCode()` 函数将 daemon 端的 string error_code 映射为 CLI 进程的 int 退出码。daemon 全链路只使用 string，只在 CLI 侧做一次转换。如果反过来用 int error_code，daemon 和 CLI 之间需要维护两套枚举，增加不一致风险。

#### 2.1.4 kind 字段说明（V2.5 新增）

`kind` 是一个 **SDK 级可选字段**，用于在 `type=result` 的 envelope 中提供辅助语义标注。它不是协议级必填字段——当 `kind` 为空时，envelope **不输出该字段**。

**设计定位：**

| 维度 | 说明 |
|------|------|
| **层级** | SDK 级（非协议级）。`kind` 不影响 JSONL 协议的解析和路由逻辑。 |
| **作用域** | 仅 `type=result` 时有效。`type=error`/`warning`/`progress` 不使用 `kind`。 |
| **取值** | 开放字符串集合，由 SDK 和应用约定。当前已知值：`help`（帮助文本）、`schema`（schema 内容）。 |
| **省略条件** | `kind` 为空字符串或 `None` 时，JSON 序列化完全省略该字段。 |

**SDK 实现方式：**

```go
// Go SDK：使用 json:"kind,omitempty" 确保 zero value 不输出
type Envelope struct {
    // ...其他字段
    Kind string `json:"kind,omitempty"`
}

// variadic 参数实现向后兼容
func NewResultEnvelope(tool string, data interface{}, kind ...string) Envelope {
    var k string
    if len(kind) > 0 && kind[0] != "" {
        k = kind[0]
    }
    return Envelope{Kind: k, /* ... */}
}
```

```python
# Python SDK：使用 Optional[str] = None，to_dict() 自动省略 None
@dataclass
class Envelope:
    kind: Optional[str] = None

    @classmethod
    def result(cls, *, tool: str, data: Any, kind: str = "") -> Envelope:
        return cls(tool=tool, type=TYPE_RESULT, data=data, kind=kind if kind else None)
```

**向后兼容保证：**
- `kind` 参数在两个 SDK 中都是可选的（Go 使用 variadic `kind ...string`，Python 使用 `kind: str = ""`）。
- 不传 `kind` 时行为与 V2.4 完全一致——输出中不包含 `kind` 字段。
- Agent 消费端应**忽略未知的 `kind` 值**，不应因为 `kind` 字段存在而改变解析逻辑。

> 💡 实践注记：`kind` 的设计决策（D022）选择了在 `success()` 方法上加可选参数而非创建专用的 `schema()`/`help()` 方法。理由是子类型是开放集合——每新增一个子类型就加一个方法会导致 API 膨胀。Go 的 variadic 参数和 Python 的默认值参数都保证了向后兼容：现有调用点不需要任何改动。

---

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

**设计契约：** 为了让 Agent 能够"一套逻辑通吃所有工具"，凡遵守此规范的 CLI，必须将 `agent` 注册为一级保留命令，并实现以下两级分层命令体系：**核心命令（Required）**是 Agent 正常运转的最低保障，所有合规工具必须实现；**可选命令（Optional）**按工具架构与业务场景按需提供。**业务命令严禁占用该空间。**

> **分级原则：** Required 命令覆盖 Agent 的"感知-诊断-配置"核心闭环——不了解工具能做什么（schema）、出了什么错（errors）、当前配置是什么（config）、健康状态如何（doctor）、崩溃现场是什么（last-crash）、缓存是否干净（cache clean），Agent 就无法可靠地编排任何业务流程。Optional 命令解决的是"锦上添花"的运维场景（升级、认证、守护进程管理、功能请求、链路追踪），它们的缺失不影响核心业务链路。

### 3.1 核心命令 (Required)

以下 7 个命令是合规工具的**强制实现项**。Agent 可以在任何合规工具上无条件调用这些命令。

#### 3.1.1 语义自描述

* `[CLI] agent schema`：输出全量 JSON Schema，包含每一个业务命令的必填项、类型、描述，以及是否允许盲冲的 `"is_idempotent": true/false` 标识。**同时包含工具版本、最后更新日期、文档指引等元信息**（原 `agent info` 功能已并入此命令，Agent 无需单独调用 info 即可获取工具身份与能力全景）。

> 💡 实践注记：将 `agent info` 并入 `agent schema` 的理由是消除冗余调用——Agent 在每次新会话中首先调用 `agent schema` 获取命令边界，此时顺带获得版本和文档信息是最自然的交互模式。单独的 `agent info` 增加了一次进程启动开销却未提供增量价值。

* `[CLI] agent errors`：输出工具独有的自定义排错字典列表（`error_code` 到排障指引的映射）。

#### 3.1.2 配置管理

* `[CLI] agent config list --redact`：盘点所有挂载的配置设定（强制脱敏）。
* `[CLI] agent config set <key> <value>`：Agent 运行时热修改特定环境数值（如延长超时期）。

> 💡 实践注记：wr 的 `config set` 使用路径白名单（`ValidConfigPaths()`）防止拼写错误静默创建无效键。建议规范要求所有 `config set` 实现路径校验，而非盲目写入。

#### 3.1.3 健康诊断

* `[CLI] agent doctor`：执行系统权限、网络、守护进程健康度体检，返回确定的 Pass/Fail 状态。
* `[CLI] agent debug last-crash`：致命退出后（Exit Code 1），提取包含 Call Stack（死机堆栈）与调用参数字典的全真上下文。

#### 3.1.4 资源维护

* `[CLI] agent cache clean`：清理上一次任务遗留的缓存脏文件或长期累积的 tmp 文件。

### 3.2 可选命令 (Optional)

以下 10 个命令按工具架构与业务场景按需实现。Agent 在调用前应通过 `agent schema` 检查该命令是否存在。

#### 3.2.1 版本升级

* `[CLI] agent update check`：只读不写，获取是否有可用的二进制远端新版本，并携带变化说明（Breaking Changes）。
* `[CLI] agent update apply`：触发本程序的原地版本替换与升级校验。

#### 3.2.2 凭据管理

* `[CLI] agent auth status`：检查凭证有效性与配额。绝不能输出明文 Token，仅返回 boolean 和过期时间。

#### 3.2.3 守护进程管理

* `[CLI] agent daemon status`：查询守护进程运行状态（如果架构使用 daemon 模式）。
* `[CLI] agent daemon start`：启动守护进程（如果架构使用 daemon 模式）。
* `[CLI] agent daemon stop`：停止守护进程（如果架构使用 daemon 模式）。

#### 3.2.4 环境取证

* `[CLI] agent debug env --redact`：提取 OS 硬件环境、核心依赖情况组成的 Issue 快照。

#### 3.2.5 反馈溯源

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

### 4.6 SDK Daemon 辅助模式（V2.4 新增）

SDK 提供 daemon 辅助能力，降低 CLI→HTTP daemon 架构的接入成本。辅助能力定位为"桥接层"——SDK 不提供 daemon 框架本身（如进程管理、PID 文件、自动重启），只提供 daemon handler 编写所需的最小工具集。

#### 4.6.1 NewHTTPWriter 工厂函数

SDK 提供 `NewHTTPWriter(w http.ResponseWriter, toolName string) *Writer` 工厂函数，一行创建 daemon handler 的 JSONL Writer：

```go
func handleList(w http.ResponseWriter, r *http.Request) {
    writer := agentsdk.NewHTTPWriter(w, "my-cli") // 自动设置 Content-Type + HTTP 200
    records, err := storage.ListAll()
    if err != nil {
        writer.ErrorWithCode("storage_error", "failed to list records: "+err.Error())
        return
    }
    writer.Success(records)
}
```

**工厂函数自动完成的操作：**
- 设置 `Content-Type: application/x-ndjson` 响应头。
- 写入 HTTP 200 状态码。
- 返回一个 `*Writer` 实例，后续所有 JSONL 输出通过该实例完成。

**约束：**
- `toolName` 为空时 panic——这是编程错误，应在启动时暴露，不应在请求时静默失败。
- SDK **不提供** `DaemonHandlerFunc` adapter（决策 D018）。理由：用户的路由模式多样（`net/http`、chi、gorilla mux、gin 等），adapter 太 opinionative。工厂函数让用户保留自己的路由选择。

#### 4.6.2 App.Registry() 与 ErrorCode 映射

`App.Registry()` 返回 `*ErrorCodeRegistry`，daemon handler 和 CLI client 共享同一个 registry：

- **daemon 侧**：handler 通过 `writer.ErrorWithCode("record_not_found", ...)` 发射 JSONL 错误，error_code 来自 registry。
- **CLI client 侧**：通过 `registry.ToExitCode("record_not_found")` 将 daemon 的 string error_code 映射为 CLI 进程的 int 退出码。

这种"daemon 说 string、CLI 转 int"的模式保证了两层使用同一份 error_code 定义，不需要维护两套枚举。

> 💡 实践注记：`App.Registry()` 暴露 registry 是为了支持 daemon→CLI 的 error_code 映射。如果不暴露 registry，CLI client 包就需要硬编码 error_code→exit_code 的映射，与 daemon 侧的定义重复且容易不同步。暴露 registry 后，client 包只需调用 `registry.ToExitCode(code)` 即可。

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

> 💡 实践注记：wr 的数据目录默认 `~/.work-report/`，可通过环境变量或 `config.json` 的 `data_dir` 字段自定义。沙箱子目录（`locks/`、`crash_dumps/`、`cache/`）的名称硬编码在 `sandbox` 包中，不允许自定义。这种"外柔内刚"的策略平衡了灵活性和可预测性。web-clip-helper 最初使用 XDG 多路径模式，后发现维护成本过高，V2.3 规范统一推荐单目录模式。存量工具迁移时，只需修改 `base_dir`，不用改代码。

#### 5.5.2 跨平台路径适配（V2.2 新增，V2.3 修订）

工具**必须使用单一目录（Single Directory）模式**作为沙箱根路径，将配置、数据、缓存、锁文件等统一放在同一个目录下。禁止将不同类型的数据分散到 XDG 标准的多路径（如 config dir + data dir + state dir 分离）。

**推荐目录结构：**

```
<base_dir>/                    ← 默认 ~/.app-name/，可通过环境变量覆盖
├── config.yaml                ← 配置文件
├── data/                      ← 业务数据（使用者自定义内部结构）
├── cache/                     ← SDK 自动管理
├── locks/                     ← SDK 自动管理
├── crash_dumps/               ← SDK 自动管理
└── daemon.log                 ← SDK 自动管理（如果使用 daemon 模式）
```

**跨平台默认路径规则：**

| 操作系统 | 默认 `base_dir` |
|----------|----------------|
| Linux / macOS | `~/.app-name/` |
| Windows | `%USERPROFILE%\.app-name\` |

**规则：**
- `base_dir` 可通过环境变量 `<APP_NAME>_HOME` 或 `agent config set data_dir` 覆盖。
- **所有平台默认使用 `~/.app-name/` 模式**，不使用 XDG 多路径分离（`~/.config/` + `~/.local/share/` + `~/.local/state/`）。理由见下。
- 沙箱内部分区目录（`locks/`、`crash_dumps/`、`cache/`、`data/`）的名称在不同平台保持一致——跨平台差异只影响 `base_dir` 的默认值，不影响内部结构。
- 工具启动时调用 `EnsureSandboxDirs(baseDir)` 创建所有必需子目录。

**为什么不用 XDG 多路径：**

| 维度 | XDG 多路径 | 单目录模式 |
|------|-----------|-----------|
| 用户心智模型 | "配置在 A，数据在 B，缓存在 C" | "一切都在 `~/.app-name/`" |
| 调试/排查 | 需要知道 3-4 个不同路径，且路径因 OS 不同 | 一个路径，所有平台统一 |
| 备份/清理 | 需要删除多个分散的目录 | 删除一个目录即可 |
| 测试隔离 | 需要 mock 多个 base path | 一个 `t.TempDir()` |
| SDK 复杂度 | 路径管理代码量大（web-clip-helper 的 `paths.py` 178 行） | 极简（wr 的等价代码约 30 行） |
| 迁移逻辑 | 旧路径→多个新路径，迁移复杂 | 结构不变，无需迁移 |

Agent-Native CLI 工具不是桌面应用——用户不会通过 OS 的备份工具管理它。它的用户是 AI Agent 和开发者，他们需要的是"一个路径、一个心理模型"。XDG 多路径带来的复杂性在这个场景下收益远低于成本。

> 💡 实践注记（V2.3）：web-clip-helper 最初采用 XDG 多路径模式（`platformdirs` 库 + `get_config_dir()`/`get_data_dir()`/`get_cache_dir()` 三路径分离），在实际开发中遇到了显著的维护负担：路径管理代码膨胀到 178 行、迁移逻辑复杂、测试需要覆盖多路径场景、调试时用户（和 Agent）难以定位数据位置。wr 采用单目录模式（`~/.work-report/`）全程无此类问题。**结论：对于面向 AI Agent 的 CLI 工具，单目录模式是更优选择。** SDK 应默认采用单目录模式，不提供 XDG 多路径选项。

#### 5.5.3 跨平台原子写入（V2.4 新增）

`ConfigManager.Save` 使用 `atomicReplace` helper 实现跨平台原子写入，防止配置文件写入过程中断导致数据损坏。

**写入策略：**

1. **首选 `os.Rename`**——在同一文件系统上，rename 是原子操作（POSIX 保证）。
2. **跨卷回退**——当 `os.Rename` 失败且错误码为跨设备（Windows errno 17 `ERROR_NOT_SAME_DEVICE`，Unix errno 18 `EXDEV`）时，回退到 `ReadFile` + `WriteFile` + `Remove` 三步操作。

**Windows 跨卷场景：** 当系统 TEMP 目录在 C: 盘而配置文件在 D: 盘时，`os.CreateTemp` 生成的临时文件与目标文件不在同一卷，`os.Rename` 会失败。SDK 通过 `runtime.GOOS` 判断平台并检查 errno 自动处理此场景，不需要 build tags。

**规则：**
- 使用 `runtime.GOOS` 运行时判断，不使用 build tags。理由：单二进制跨平台分发时 build tags 无法覆盖，运行时判断更通用。
- 回退路径使用 `os.Remove(src)` 的 error return 丢弃（`_ = os.Remove(src)`）——临时文件残留不影响功能，下次启动会清理。
- 不使用 `syscall.Rename`——`os.Rename` 在 Go runtime 层已经做了平台适配。

> 💡 实践注记：wr 的 `atomicReplace` 在 Windows 开发环境（TEMP=C:，配置=D:）触发过跨卷 rename 失败。最初的实现直接返回 rename error，导致 `config set` 在 Windows 上失败。修复后增加了 errno 17/18 的检测和 ReadFile+WriteFile 回退路径。测试时需要覆盖同卷（直接 rename 成功）和跨卷（回退路径）两种场景——跨卷测试可以通过在 Windows 上设置 `TMP` 环境变量到不同驱动器来触发，或在 Unix 上使用 bind mount。

### 5.6 结构化错误类型模式（V2.4 新增）

SDK 定义 `WhitelistError` 和 `UnknownFieldError` 两个接口，使用 Go 的 marker-method 惯例（同 `net.Error`），允许调用方通过 `errors.As()` 精确判断错误类型，而不依赖字符串匹配。

#### 5.6.1 接口定义

```go
// WhitelistError 表示字段不在配置白名单中。
type WhitelistError interface {
    error
    Field() string           // 返回被拒绝字段的 json-path 名称
    IsWhitelistError() bool  // marker method，用于 errors.As 匹配
}

// UnknownFieldError 表示字段名在配置结构体中不存在。
type UnknownFieldError interface {
    error
    Field() string              // 返回未知字段的 json-path 名称
    IsUnknownFieldError() bool  // marker method，用于 errors.As 匹配
}
```

#### 5.6.2 使用规则

**必须使用 `errors.As()` 判断错误类型，禁止 `strings.Contains` 匹配错误消息。**

```go
// ✅ 正确：使用 errors.As 类型断言
err := configManager.SetByPath("secret_field", "value")
var whitelistErr agentsdk.WhitelistError
if errors.As(err, &whitelistErr) {
    // 精确知道是白名单拒绝，可以获取 whitelistErr.Field()
}

// ❌ 错误：使用字符串匹配
if strings.Contains(err.Error(), "not in whitelist") {
    // 脆弱：错误消息变更就会破坏判断逻辑
}
```

#### 5.6.3 第三方 ConfigProvider 适配

第三方 ConfigProvider（不 import SDK 类型）只需实现方法签名即可满足接口，不需要 import SDK 包。这是 marker-method 惯例的核心优势——接口满足基于方法集，不基于类型导入：

```go
// 第三方 ConfigProvider 的自定义错误类型
type myProviderError struct {
    field string
}

func (e *myProviderError) Error() string              { return "blocked: " + e.field }
func (e *myProviderError) Field() string              { return e.field }
func (e *myProviderError) IsWhitelistError() bool     { return true }

// SDK 侧通过 errors.As 自动匹配
var wlErr agentsdk.WhitelistError
if errors.As(err, &wlErr) {
    // 即使 err 来自第三方包，也能匹配
}
```

> 💡 实践注记：`ConfigManager.SetByPath` 在路径校验失败时返回 `WhitelistError`（字段存在但不在白名单中）或 `UnknownFieldError`（字段名不存在）。CLI 的 `agent config set` 命令根据这两个接口将错误分类为 `INPUT_INVALID`（退出码 2），Agent 可以据此区分"拼写错误"和"权限不足"。SDK 测试中包含一个 `thirdPartyWhitelistError` 用例，验证第三方类型不需要 import SDK 即可满足接口。

### 5.7 测试隔离模式（V2.4 新增）

SDK 提供测试辅助设施，确保测试不依赖外部文件系统状态，每个测试用例完全隔离。

#### 5.7.1 NewTestApp 与 Functional Options

`NewTestApp(name, version string, opts ...TestOption)` 创建一个用于测试的 `App` 实例，输出写入 `bytes.Buffer` 而非 stdout，便于断言 JSONL 内容：

```go
func TestMyCommand(t *testing.T) {
    app, buf := agentsdk.NewTestApp("my-cli", "1.0")
    cmd := app.AgentCommands()
    cmd.SetArgs([]string{"schema"})
    cmd.Execute()
    // buf.String() 包含完整的 JSONL 输出
    envs, err := agentsdk.ParseEnvelopes(buf.String())
    // ...断言 envs 的内容和类型
}
```

**Functional Options：**
- `WithTmpDir(dir string)` — 覆盖 sandbox 目录到指定路径。通常与 `t.TempDir()` 配合使用，确保测试产生的文件在测试结束后自动清理。

```go
func TestWithRealFiles(t *testing.T) {
    tmp := t.TempDir()
    app, buf := agentsdk.NewTestApp("my-cli", "1.0", agentsdk.WithTmpDir(tmp))
    // app 的 sandbox 指向 tmp，所有文件操作都在 tmp 内
}
```

#### 5.7.2 AgentCommands 扩展钩子

`AgentCommands(extra ...*cobra.Command)` 接受可变数量的额外 `*cobra.Command`，用于在测试中注册业务命令：

```go
func TestBusinessCommand(t *testing.T) {
    app, buf := agentsdk.NewTestApp("my-cli", "1.0")
    myCmd := &cobra.Command{Use: "my-action", Run: myActionHandler}
    rootCmd := &cobra.Command{Use: "my-cli"}
    rootCmd.AddCommand(app.AgentCommands(myCmd))
    rootCmd.SetArgs([]string{"agent", "my-action", "--flag", "value"})
    rootCmd.Execute()
}
```

**约束：**
- `extra` 参数追加到 `agent` 命令的子命令列表中，不影响 SDK 内置的 `schema`、`config`、`doctor` 等命令。
- 不传 `extra` 时行为与旧版完全兼容（向后兼容保证）。
- 传入的 `extra` 命令会自动出现在 `agent schema` 的输出中。

> 💡 实践注记：`NewTestApp` + `WithTmpDir` + `AgentCommands` 三者组合提供了完整的测试隔离——App 实例隔离（bytes.Buffer 输出）、文件系统隔离（t.TempDir）、命令注册隔离（按需注入）。测试不需要启动真实的 HTTP server 或修改全局状态。`ParseEnvelopes` 和 `MustParseEnvelopes` 辅助函数将 JSONL 字符串解析为 `Envelope` 结构体切片，便于在测试中断言 `type`、`error_code`、`data` 等字段。

#### 5.7.3 Python reset_for_testing() 模式（V2.5 新增）

Python SDK 提供 `App.reset_for_testing()` 方法，用于在测试用例之间重置运行时状态。与 Go 的 `NewTestApp`（创建全新实例）不同，Python 采用"重置已有实例"的模式，更适合 pytest fixture 生命周期管理。

**重置边界（D024）：**

`reset_for_testing()` 遵循严格的重置边界——只重置**运行时状态**（两次 `run()` 调用之间累积的状态），不重置 **setup-time 注册**（import/模块初始化阶段注册的配置）：

| 类别 | 重置？ | 说明 |
|------|--------|------|
| `writer` | ✅ 重置 | 替换为新的 `Writer(io.StringIO())`，清除缓冲区 |
| `flight_context` | ✅ 重置 | 调用 `FlightContext.clear()` 清除所有 key-value |
| `fake_stream` | ✅ 重置 | 设为 `None`，`captured_output` 返回 `""` |
| `error_code` registry | ❌ 保留 | 内置 + 自定义 error_code 注册在 setup-time |
| `config_providers` | ❌ 保留 | 配置提供者在 setup-time 注册 |
| `health_checks` | ❌ 保留 | 健康检查函数在 setup-time 注册 |
| `command_meta` | ❌ 保留 | 命令元数据在 setup-time 注册 |
| `on_help` callback | ❌ 保留 | setup-time 钩子，per D024 |

**设计理由：** setup-time 注册是"测试环境搭建"的一部分（类似 `conftest.py` 中的 fixture），如果在每个测试用例之间重置它们，测试需要重复注册相同的 error_code、config_provider 和 health_check——这违反了 DRY 原则且容易遗漏。

```python
import pytest
from agentsdk import App

@pytest.fixture
def app():
    a = App("my-cli", "1.0")
    a.register_error_code("custom_error", 10, "A custom error")
    a.on_help(lambda text: None)  # setup-time hook
    yield a
    # no teardown needed — reset happens per-test

def test_first_command(app):
    app.reset_for_testing()
    code = app.run(["agent", "schema"])
    assert code == 0

def test_second_command(app):
    app.reset_for_testing()  # clean slate, but error_code + on_help survive
    code = app.run(["agent", "doctor"])
    assert code == 0
```

#### 5.7.4 on_help 钩子（V2.5 新增）

`App.on_help(callback)` 注册一个在 `--help` 输出被捕获时调用的回调函数。这是 setup-time 注册，通过 `reset_for_testing()` 保留。

**工作流程：**

1. 当 CLI 参数包含 `--help` 时，Python SDK 设置 `help_invoked = "--help" in args`。
2. Click 框架在 `standalone_mode=False` 下将帮助文本输出到 stdout（不抛 `SystemExit`）。
3. 运行结束后，如果 `exit_code == 0` 且 `help_invoked == True`，SDK 检查是否有 `on_help` 回调：
   - **有回调**：调用 `callback(captured_text)`，由回调决定如何处理。
   - **无回调**：自动包装为 `kind="help"` 的 result envelope 发射。

**关键实现细节：**

- **`--help` 检测用 args 检查，不用 exit code**。Click 在 `standalone_mode=False` 下不抛 `SystemExit`，因此不能通过捕获 `SystemExit` 来检测 help 调用（见陷阱 13）。
- **正常命令输出不被误判为 help**。只有当 `args` 中显式包含 `--help` 且退出码为 0 时才触发 help 处理逻辑。
- **回调抑制默认行为**。注册 `on_help` 回调后，默认的 `kind="help"` envelope 自动包装行为被抑制——回调完全接管 help 文本的处理。

```python
app = App("my-cli", "1.0")
help_texts = {}

def capture_help(text: str) -> None:
    help_texts["captured"] = text

app.on_help(capture_help)

code = app.run(["my-cli", "--help"])
# help_texts["captured"] 现在包含帮助文本
# 不会自动发射 kind="help" envelope
```

> 💡 实践注记：`on_help` 钩子的典型用途是在测试中捕获 help 文本用于断言，或在集成场景中将 help 文本转发到自定义输出通道（如 LLM 上下文窗口）。它是 setup-time 注册而非 runtime 行为，因此适合在 `conftest.py` 或模块初始化时配置一次，而非每个测试用例中重复注册。

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

### 6.4 数据迁移策略（V2.2 新增，V2.3 修订）

存量工具在升级沙箱目录布局时（如增加新的内部分区、变更目录结构），需要将历史数据从旧布局迁移到新布局。迁移必须**零数据丢失**。

由于本规范采用单一目录模式（见 5.5.2），迁移只涉及同一 `base_dir` 内部的结构调整，不涉及跨目录的数据搬运。

#### 6.4.1 复制 + 标记模式

迁移**必须使用复制（copy）而非移动（move）**，配合标记文件：

```
~/.app-name/                        ← base_dir 不变
├── config.yaml                     ← 保留原位
├── records.db                      ← 旧版：数据文件散落在根目录
├── records/
├── .migrated-to-v2                 ← 迁移完成后创建的标记文件
│
│   迁移后：
├── config.yaml                     ← 不动
├── data/                           ← 新版：业务数据统一放入 data/
│   ├── records.db                  ← 从根目录复制而来
│   └── records/
├── cache/                          ← 新增分区
├── locks/                          ← 新增分区
└── crash_dumps/                    ← 新增分区
```

**规则：**
1. 迁移前检查标记文件（如 `.migrated-to-v2`）。若已存在则跳过迁移。
2. 在 `base_dir` 内部复制/移动文件到新分区。原始文件在确认迁移成功前保持不变。
3. 复制成功后，在 `base_dir` 根目录创建标记文件。
4. 后续所有操作使用新的目录结构。
5. **不删除旧文件**——保留作为回退保障，直到用户手动清理或通过 `agent cache clean` 清理。

**理由：** 移动操作在失败时不可恢复（如磁盘满、权限不足）。复制确保数据始终存在，即使新布局出问题也能回退。标记文件防止重复迁移。

#### 6.4.2 迁移失败处理

- 任何文件复制失败 → 停止迁移，使用旧布局继续运行，输出 `type=warning` 提示迁移未完成。
- 标记文件存在但数据不完整 → 删除标记文件，下次启动时重新尝试。
- **永远不要因为迁移失败而阻止工具正常运行。**

#### 6.4.3 从 XDG 多路径迁移到单目录模式

如果存量工具此前采用了 XDG 多路径模式（如 `~/.config/<app>/` + `~/.local/share/<app>/` + `~/.local/state/<app>/` 分离），迁移到单目录模式时需额外处理：

1. 将所有分散路径的数据**复制**到统一的 `base_dir` 中。
2. 在旧路径的每个根目录下创建 `.migrated-to-single-dir` 标记文件。
3. 后续所有操作只读写 `base_dir`。
4. 旧路径数据保留不删除。
5. 如果多个旧路径存在同名文件（如 config），使用数据量更大的版本（或保留两个并重命名）。

> 💡 实践注记：web-clip-helper 最初使用 XDG 多路径模式（`platformdirs` 库），`paths.py` 中的 `ensure_dirs()` 自动触发旧路径→新路径迁移。迁移覆盖 config.yaml、clips.db、clips/ 目录和 reports/ 目录。如果目标文件已存在则跳过（幂等性）。但多路径迁移逻辑复杂，占据了 `paths.py` 大量篇幅。**V2.3 结论：后续版本应迁移到单目录模式，消除多路径带来的维护负担。SDK 应直接采用单目录模式，不提供 XDG 多路径选项。**

---

## 附录 A：标准 Agent System Prompt (适配器调用逻辑示例)

为了最大化发挥本规范价值，请在唤起大语言模型 Agent 时植入以下框架逻辑：

> **🌍 面向 AI Agent 的 CLI 操作铁律：**
> 1. 新接触本系统任意命令前，严禁产生"常识性幻觉假设"，必须首选执行 `agent schema` 获取正确参数和动作边界。
> 2. 对于 `is_idempotent: false` 及高危数据操作，强制先附带 `--dry-run`；对于资源密集型操作前先通过 `agent doctor` 体检。
> 3. 若目标由于网络/环境抛出可修复错误代码 (例如 Exit 3 或 4)，根据 `agent errors` 诊断建议重试或修正。**Exit 3 通常表示 daemon 未启动——先执行 `agent daemon start`。**
> 4. 若目标由于底层缺陷抛出不可逆代码 (Exit 1)，即刻调用 `agent debug last-crash` 加 `agent debug env --redact` 调取线索，并停止当前流程，输出完整 Bug 报告。
> 5. 系统能力无法直接完成推演的动作，不要虚构调用，立即使用 `agent feature record`（Optional 命令，通过 `agent schema` 确认可用性）归档此功能痛点请求。
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

### 陷阱 11：Windows 跨卷 atomicReplace 失败（V2.4 新增）

**症状：** `agent config set` 在 Windows 上返回错误，但同样的代码在 Linux/macOS 上正常工作。
**原因：** Windows 上系统 TEMP 目录（`os.TempDir()`）与配置文件不在同一卷（如 TEMP 在 C:，配置在 D:），`os.Rename` 返回 errno 17 `ERROR_NOT_SAME_DEVICE`。
**修复：** 使用 `atomicReplace` helper，检测 errno 17（Windows）或 errno 18 `EXDEV`（Unix）后回退到 `ReadFile` + `WriteFile` + `Remove`。不使用 build tags，用 `runtime.GOOS` 运行时判断。

### 陷阱 12：第三方错误类型无法通过 errors.As 匹配（V2.4 新增）

**症状：** 自定义 ConfigProvider 返回的错误无法被 SDK 的错误分类逻辑识别，导致 `agent config set` 对合法的白名单拒绝返回通用错误码。
**原因：** 错误类型使用字符串消息匹配（`strings.Contains(err.Error(), "whitelist")`），而非 marker-method 接口。
**修复：** 使用 marker-method 惯例定义错误接口（`WhitelistError`、`UnknownFieldError`），第三方只需实现方法签名即可满足接口。用 `errors.As()` 判断，不用 `strings.Contains` 匹配消息。

### 陷阱 13：on_help 检测不能依赖 exit code（V2.5 新增）

**症状：** Python SDK 中 `--help` 输出没有被正确捕获或包装为 `kind="help"` envelope，导致 Agent 收到裸文本而非 JSONL。
**原因：** 开发者试图通过捕获 `SystemExit` 来检测 `--help` 调用（类似 argparse 的行为），但 Click 框架在 `standalone_mode=False` 下**不会抛出 `SystemExit`**——它将帮助文本直接输出到 stdout 并正常返回。
**修复：** 使用 args 检查而非 exit code 检查。在 `run()` 入口处记录 `help_invoked = "--help" in (args or [])`，运行结束后结合 `exit_code == 0 && help_invoked` 来判断是否为 help 调用。这种方式不依赖框架特定的异常行为，对所有 Python CLI 框架都适用。

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
