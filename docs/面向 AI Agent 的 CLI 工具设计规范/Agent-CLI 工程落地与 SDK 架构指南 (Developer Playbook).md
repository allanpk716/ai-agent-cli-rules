# 🛠️ Agent-CLI 工程落地与 SDK 架构指南 (Developer Playbook)

> **导语**：本文档是《面向 AI Agent 的 CLI 工具设计规范》的工程配套指南。基于 [wr](https://github.com/nanobot-io/work-report)（Go 1.24 + Cobra + HTTP Daemon）的真实落地经验，解答一线研发团队在改造存量 CLI 或开发新 CLI 时，如何以最低重构成本、最安全的方式落实 100% JSONL 输出、优雅退出及保留命名空间等硬性约束。

## 架构全景

```
┌──────────────────────────────────────────────────────┐
│  AI Agent (调用方)                                    │
│  $ wr add --text "下午三点和 Alice 开会"               │
└──────────────────┬───────────────────────────────────┘
                   │ os.Exit(code)
                   │ stdout: JSONL only
┌──────────────────▼───────────────────────────────────┐
│  Thin CLI (cmd/)                                      │
│  ┌──────────┐  ┌──────────────┐  ┌───────────────┐  │
│  │ 参数解析   │→│ HTTP Client  │→│ JSONL 透传    │  │
│  │ (Cobra)   │  │ (client.go)  │  │ (jsonl.go)   │  │
│  └──────────┘  └──────┬───────┘  └───────────────┘  │
└───────────────────────┼──────────────────────────────┘
                        │ 127.0.0.1:17530
┌───────────────────────▼──────────────────────────────┐
│  HTTP Daemon (daemon/)                                │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────┐   │
│  │ Router    │→│ Handlers │→│ Storage / LLM /   │   │
│  │ (ServeMux)│  │ (业务逻辑) │  │ Pushover / Schedule│ │
│  └──────────┘  └──────────┘  └──────────────────┘   │
│                 ┌──────────────────────────┐          │
│                 │ panicRecoveryMiddleware   │          │
│                 └──────────────────────────┘          │
└──────────────────────────────────────────────────────┘
                        │
              ┌─────────┼──────────┐
              ▼         ▼          ▼
        ~/.work-report/work-records/   ~/.work-report/
        (JSON 文件存储)    ┌────────┬──────────┐
                          │ locks/ │crash_dumps│
                          │ cache/ │daemon.log │
                          └────────┴──────────┘
```

---

## 一、核心架构策略：全面 SDK 化，拒绝业务层手写

规范要求实现 `agent xxx` 保留元命令，分为核心（Required）和可选（Optional）两级：7 个核心命令覆盖感知-诊断-配置闭环，10 个可选命令提供版本升级、凭据管理、守护进程、环境取证、反馈溯源等扩展能力。如果让业务代码各自实现，会导致严重的逻辑冗余与规范腐化。

### 1.1 SDK 封装三件套

wr 将核心能力封装在三个 internal 包中，形成可复用的 SDK 底座：

| 包 | 职责 | 对外 API |
|---|---|---|
| `internal/jsonl` | JSONL 信封格式 + 唯一输出出口 | `Success()`, `Error()`, `ErrorWithCode()`, `Warning()`, `ProgressEnvelope()` |
| `internal/exitcode` | 语义退出码定义 + `ExitError` 类型 | `ExitSuccess`, `ExitFatalError`, `FromErrorCode()` |
| `internal/sandbox` | 沙箱目录管理 | `EnsureSandboxDirs()`, `OpenDaemonLog()` |

### 1.2 JSONL 信封规范 — 唯一输出出口

所有 CLI 输出（包括错误）通过 `internal/jsonl` 包的 `Writer` 发出，绝不直接 `fmt.Println` 到 stdout。信封结构：

```go
// internal/jsonl/output.go
type Envelope struct {
    Version   string      `json:"version"`             // "1.0"
    Tool      string      `json:"tool"`                // "wr"
    Type      string      `json:"type"`                // "result" | "error" | "warning" | "progress"
    Timestamp string      `json:"timestamp"`           // RFC3339Nano
    Data      interface{} `json:"data,omitempty"`      // 成功时的载荷
    ErrorCode string      `json:"error_code,omitempty"` // 错误码（snake_case）
    Message   string      `json:"message,omitempty"`   // 人类可读消息
    Percent   int         `json:"percent,omitempty"`   // 进度百分比
}
```

业务代码使用方式极简：

```go
// 成功输出
jsonl.Success(map[string]interface{}{"id": "abc123"})

// 带错误码的失败
jsonl.ErrorWithCode("invalid_type", "missing required field: type")

// 进度
env := jsonl.ProgressEnvelope(50, "processing...")
jsonl.DefaultWriter.WriteEnvelope(env)
```

**`omitempty` 的妙用**：成功信封不会携带 `error_code`/`message`，错误信封不会携带 `data`。AI Agent 只需检查 `type` 字段即可判断结果类别，无需猜测哪些字段存在。

### 1.3 Cobra 命令注册 — 业务降级为 Handler

业务代码只定义 Cobra 命令的参数和 `RunE` 函数，不掌控入口：

```go
// cmd/list.go — 典型的 thin handler
var listCmd = &cobra.Command{
    Use:   "list",
    Short: "List work report entries",
    RunE: func(cmd *cobra.Command, args []string) error {
        path := "/api/list"
        // ... 拼接查询参数 ...
        return client.CallDaemonGet(os.Stdout, path)
    },
}

func init() {
    listCmd.Flags().StringVar(&listType, "type", "", "Filter by entry type")
    rootCmd.AddCommand(listCmd)
}
```

整个 CLI 的入口只有 7 行：

```go
// main.go
func main() {
    os.Exit(cmd.Execute())
}
```

> **wr 实践注记**：wr 没有实现"反射自动 schema"——这对 Go 的静态类型系统不友好。而是通过 `wr status`（配置诊断）和结构化的 JSONL 输出让 Agent 自行发现可用操作。实际 Agent 调用时，一条 `wr list` 就能拿到所有记录的结构化数据，比 schema 推断更直接。

---

## 二、核心技术攻坚方案 (The 5 Engineering Pillars)

### 1. I/O 物理级静音与发声筒机制 (I/O Hijacking)

#### 理论方案

为防止历史遗留库或第三方 C 库的 `print()` 破坏 JSONL 纯净度：
- 入口劫持：SDK 加载时将 `stdout`/`stderr` 重定向至内存缓冲
- 唯一出口：业务通过 `emitter.result()` / `emitter.error()` 输出
- Panic Catcher：全局 panic 拦截转为 `FATAL_CRASH` 信封

#### wr 的落地实现

wr 采用 **"不劫持 + 唯一出口 + 双层防护"** 的策略：

**唯一出口** — `jsonl.Writer` 是 stdout 的唯一写入者：

```go
// internal/jsonl/output.go
type Writer struct {
    out io.Writer
}

func (w *Writer) write(env Envelope) error {
    b, err := json.Marshal(env)
    if err != nil {
        return fmt.Errorf("jsonl: marshal: %w", err)
    }
    _, err = fmt.Fprintf(w.out, "%s\n", b)
    return err
}
```

**CLI 层 panic 防护** — `cmd.Execute()` 包裹 recover：

```go
// cmd/root.go
func Execute() (code int) {
    defer func() {
        if r := recover(); r != nil {
            jsonl.DefaultWriter.ErrorWithCode("FATAL_CRASH",
                fmt.Sprintf("panic: %v", r))
            code = exitcode.ExitFatalError
        }
    }()
    if err := rootCmd.Execute(); err != nil {
        // ... ExitError 提取（见第五章）...
    }
    return exitcode.ExitSuccess
}
```

**Daemon 层 panic 防护** — HTTP middleware 捕获所有 handler panic：

```go
// internal/daemon/server.go
func (s *Server) panicRecoveryMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if err := recover(); err != nil {
                env := jsonl.ErrorEnvelope("FATAL_CRASH",
                    fmt.Sprintf("panic: %v", err))
                writeEnvelope(w, env) // 写入 HTTP 响应
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

> **wr 实践注记**：wr 没有劫持 `os.Stdout`——Go 的第三方库（Cobra、标准库）不会随意 print 到 stdout，没必要付出劫持的复杂度。双层 panic 防护（CLI + Daemon）已经覆盖了所有 panic 场景。

> **Python/Node 生态实践注记**：web-clip-helper（Python + Typer）**必须劫持 stdout**。原因：(1) Click/Typer 的参数验证错误会直接将 Rich 格式文本写入 stdout；(2) `NoArgsIsHelpError` 在异常 `__init__` 阶段就把帮助文本泄漏到 stdout，早于任何拦截层；(3) LLM SDK（如 OpenAI Python 客户端）的 debug 日志会污染输出流。
>
> 实现模式：在 CLI 入口的最早时机（`_JSONLGroup.main()` 覆写中），将 `sys.stdout` 替换为自定义的 `_FakeStream` 对象。`_FakeStream` 使用**组合模式**（包装一个内部 buffer），而非继承 `io.TextIOBase`——后者会导致 `buffer`、`encoding`、`newlines` 等属性遮蔽问题。真实 stdout 保存在模块私有变量中，只有 `jsonl_emit()` 函数有权写入。
>
> 关键教训：(1) 劫持时机必须早于 Click 的参数解析——在 `main()` 方法覆写的第一行就替换，而不是在 `invoke()` 中；(2) `--help` 检测不能通过捕获异常（Click 的 `standalone_mode=False` 会把 `Exit(0)` 当 `RuntimeError` 抛出），必须在成功路径上检测输出内容是否为非 JSONL。

---

### 2. 状态黑匣子与工作区规范 (Sandbox Workspace)

#### 理论方案

遵循单一目录模式，创建专属沙箱，内部严格分区：`locks/`、`crash_dumps/`、`cache/`。业务代码禁止在 pwd 乱写隐藏文件。所有数据统一放在 `~/.app-name/` 下，不使用 XDG 多路径分离。

#### wr 的落地实现

沙箱目录创建是一次性幂等操作：

```go
// internal/sandbox/sandbox.go
const (
    DirLocks      = "locks"
    DirCrashDumps = "crash_dumps"
    DirCache      = "cache"
)

func EnsureSandboxDirs(baseDir string) error {
    for _, sub := range sandboxDirs {
        path := filepath.Join(baseDir, sub)
        if err := os.MkdirAll(path, 0755); err != nil {
            return fmt.Errorf("sandbox: cannot create directory %s: %w", path, err)
        }
    }
    return nil
}
```

Daemon 启动时创建沙箱并打开日志文件：

```go
// cmd/daemon.go — daemon start 命令
baseDir := filepath.Join(home, ".work-report")

if err := sandbox.EnsureSandboxDirs(baseDir); err != nil {
    return writeExitError(exitcode.ExitFatalError,
        fmt.Sprintf("cannot create sandbox dirs: %v", err))
}

logFile, err := sandbox.OpenDaemonLog(baseDir)
// ...
log.SetOutput(logFile) // 所有 log.Printf 输出重定向到 daemon.log
```

实际目录结构：

```
~/.work-report/
├── config.json          # 用户配置（API keys 等敏感信息）
├── .daemon.json         # daemon 运行状态（port + pid）
├── .daemon.json.bak     # 状态文件备份（防损坏）
├── daemon.log           # daemon 运行日志（append-only）
├── work-records/        # 数据存储区
│   ├── meetings/YYYY/MM/DD/*.json
│   ├── tasks/active/*.json
│   ├── tasks/completed/YYYY/MM/DD/*.json
│   ├── reminders/active/*.json
│   ├── reminders/completed/YYYY/MM/DD/*.json
│   └── logs/YYYY/MM/DD/*.json
├── locks/               # 进程互斥锁
├── crash_dumps/         # 崩溃现场
└── cache/               # 资源缓存
```

> **wr 实践注记**：wr 将沙箱放在 `~/.work-report/`（单一目录模式），配置文件和数据文件统一管理。相比 XDG 多路径分离（`~/.config/` + `~/.local/share/` + `~/.local/state/`），单目录模式显著降低了路径管理复杂度、迁移成本和用户认知负担。web-clip-helper 最初使用 XDG 多路径模式，实际维护中发现路径管理代码膨胀、调试排查困难，V2.3 规范已统一推荐单目录模式。`crash_dumps/` 目录目前预留但尚未写入——wr 通过 daemon.log + JSONL 错误信封提供了足够的诊断信息。真正的 `.last-crash.json` 机制（见 Pillar 4）需要在 daemon 层实现。

---

### 3. 解耦演习模式 (Command Pattern for Dry-Run)

#### 理论方案

所有写操作重构为"计划 (Plan)"与"执行 (Apply)"两阶段。`--dry-run` 拦截 Plan 并输出 JSON 后退出 0。

#### wr 的落地实现

wr 目前**没有实现** `--dry-run`。这是一个有意识的取舍：

- wr 的写操作（add/complete/cancel/update）都是单条记录的原子操作，不像批量操作有复杂的副作用链
- LLM 分类是只读的（不改变状态），不需要 dry-run
- 未来如果引入批量导入（如 `wr import --file records.jsonl`），dry-run 会成为必须

如果实现，模式应该是：

```go
// 假设的 dry-run 实现（wr 尚未实现）
type ExecutionPlan struct {
    Action  string      `json:"action"`
    Records []PlanEntry `json:"records"`
}

type PlanEntry struct {
    ShortID string `json:"short_id"`
    Type    string `json:"type"`
    Title   string `json:"title"`
    Date    string `json:"date"`
}

func planAdd(req addRequest, store *storage.Storage) (*ExecutionPlan, error) {
    rec := buildRecord(req)
    return &ExecutionPlan{
        Action:  "add",
        Records: []PlanEntry{{ShortID: "pending", Type: req.Type, Title: req.Title, Date: req.Date}},
    }, nil
}

func applyAdd(store *storage.Storage, rec interface{}) (interface{}, error) {
    return store.AddRecord(rec)
}

// Handler 中
func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
    // ... 解析请求 ...
    if dryRun {
        plan, _ := planAdd(req, s.storage)
        writeEnvelope(w, jsonl.SuccessEnvelope(plan))
        return
    }
    result, err := applyAdd(s.storage, buildRecord(req))
    // ...
}
```

> **wr 实践注记**：对于"单条 CRUD + LLM 分类"的工具，Plan/Apply 的分离成本高于收益。但规范要求 `--dry-run` 是硬约束——如果 CLI 面向的 Agent 会执行不可逆操作（如批量删除、发送通知），dry-run 必须实现。wr 的 `report push` 子命令就是一个典型场景：应该支持 `--dry-run` 只生成报告不发送。

---

### 4. 零时差优雅遗言 (Graceful Shutdown)

#### 理论方案

维护 `Agent_Flight_Context` 并发安全字典，SIGTERM 信号到达时放弃回滚，直接 dump 到 `.last-crash.json` 后 `exit(1)`。

#### wr 的落地实现

wr 的 daemon 使用标准 Go 模式实现优雅关闭：

```go
// internal/daemon/server.go
func (s *Server) Start(ctx context.Context, onReady func()) error {
    s.http = &http.Server{
        Addr:    fmt.Sprintf(":%d", s.port),
        Handler: s.panicRecoveryMiddleware(s.loggingMiddleware(s.router)),
    }

    // Context 取消触发关闭
    go func() {
        <-ctx.Done()
        s.shutdown()
    }()

    // 信号监听
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-sigCh
        s.shutdown()
    }()

    if onReady != nil {
        onReady()
    }

    return s.http.ListenAndServe()
}

func (s *Server) shutdown() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = s.http.Shutdown(ctx) // 等待进行中的请求完成
}
```

Daemon 启动时注册的信号处理额外做了资源清理：

```go
// cmd/daemon.go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
go func() {
    <-sigCh
    jsonl.Warning("daemon shutting down")
    sched.Stop()      // 停止定时器
    cancel()          // 取消 context
}()
```

状态文件容错——daemon 的 `.daemon.json` 有备份机制：

```go
// internal/daemon/state.go
func WriteState(dir string, state DaemonState) error {
    bakData, _ := json.Marshal(state)
    // 先写备份
    os.WriteFile(BackupPath(dir), bakData, 0644)
    // 再写主文件
    os.WriteFile(StatePath(dir), bakData, 0644)
    return nil
}

func ReadState(dir string) (DaemonState, error) {
    // 先尝试主文件，失败则读备份
    state, err := readStateFile(StatePath(dir))
    if err == nil {
        return state, nil
    }
    return readStateFile(BackupPath(dir))
}
```

CLI 层还处理了"stale state"场景——daemon 非正常退出后状态文件残留：

```go
// internal/client/client.go
resp, err := httpClient.Do(req)
if err != nil {
    // 状态文件存在但 daemon 不可达 — 清理残留
    _ = daemon.RemoveState(dir)
    return writeDaemonError(w,
        "daemon not running (stale state cleaned): daemon unreachable at port %d",
        state.Port)
}
```

> **wr 实践注记**：wr 没有实现 `.last-crash.json` dump 机制。实际的崩溃诊断依赖两个信号源：(1) daemon.log 中的 `log.Printf` 日志，(2) CLI 输出的 JSONL 错误信封（包含 `error_code: FATAL_CRASH`）。对于 wr 的复杂度，这已经足够。如果 CLI 有长时间运行的有状态操作（如批量导入数千条记录），`.last-crash.json` 变得必要——Agent 需要知道断点在哪以便恢复。

---

### 5. JSONL 一致性验证（新增 Pillar）

AI Agent 解析 JSONL 输出时，信封结构的正确性至关重要。wr 提供了 `ValidateEnvelope` 共享 helper，在测试中强制执行：

```go
// internal/jsonl/output.go
func ValidateEnvelope(env Envelope) error {
    if env.Version == "" {
        return fmt.Errorf("jsonl: envelope missing version")
    }
    if env.Tool == "" {
        return fmt.Errorf("jsonl: envelope missing tool")
    }
    if env.Type == "" {
        return fmt.Errorf("jsonl: envelope missing type")
    }
    if env.Timestamp == "" {
        return fmt.Errorf("jsonl: envelope missing timestamp")
    }
    switch env.Type {
    case TypeResult, TypeError, TypeWarning, TypeProgress:
        // valid
    default:
        return fmt.Errorf("jsonl: envelope has invalid type: %q", env.Type)
    }
    return nil
}
```

每个测试用例都验证输出的每一行：

```go
// cmd/exitcode_test.go — 共享验证 helper
func validateAllEnvelopes(t *testing.T, data []byte) {
    t.Helper()
    for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
        if line == "" { continue }
        var env jsonl.Envelope
        json.Unmarshal([]byte(line), &env)
        if err := jsonl.ValidateEnvelope(env); err != nil {
            t.Errorf("envelope validation failed: %v; line=%s", err, line)
        }
    }
}
```

> **wr 实践注记**：`ValidateEnvelope` 不验证 `data`/`error_code` 的互斥性——这由 `omitempty` JSON tag 保证。测试覆盖了字段分离（`TestEnvelopeFieldSeparation`），确保成功信封不携带 `error_code`，错误信封不携带 `data`。

---

## 三、存量项目的迁移演进图 (Migration Roadmap)

对于历史积淀深厚的旧版 CLI 工具，改造不可一蹴而就。基于 wr 的实际开发经验，建议分 4 个阶段平滑推进：

### Phase 1：核心功能闭环 (Week 1)

**目标**：能跑起来，输出是 JSONL。

- [ ] 搭建 Thin CLI + Daemon 分离架构
- [ ] 实现 JSONL 信封格式（`internal/jsonl`）
- [ ] 实现 `ExitError` + 退出码映射（`internal/exitcode`）
- [ ] 核心命令的 happy path：add / list / status / report
- [ ] CLI 层 `Execute()` 中的 panic → `FATAL_CRASH` 转换
- [ ] Daemon 层 `panicRecoveryMiddleware`
- [ ] 基础测试：exit code 映射 + envelope 验证

**交付标准**：`wr add --type task --title "test" --date 2026-01-01` 输出合法 JSONL，退出码为 0 或语义错误码。

### Phase 2：协议对齐与诊断基建 (Week 2-3)

**目标**：规范完整性，Agent 能自助诊断。

- [ ] 实现 `wr status`（配置完整性诊断 + daemon 状态）
- [ ] 建立沙箱机制：`~/.cli-name/` 目录结构
- [ ] Daemon 状态文件 + 备份 + stale state 清理
- [ ] Daemon 优雅关闭（SIGTERM/SIGINT 处理）
- [ ] 配置脱敏输出（`config.Redacted()`）
- [ ] 完善所有 error_code → exit code 映射
- [ ] 客户端超时处理（5s default）
- [ ] Daemon 健康检查端点 `/health`

**交付标准**：Agent 能通过 `wr status` 判断所有前置条件是否满足，无需人工检查。

### Phase 3：数据管理与导入导出 (Week 3-4)

**目标**：数据可靠性，支持批量操作。

- [ ] 实现 `wr import`（两阶段验证：先校验全部，再逐条写入）
- [ ] 实现 `wr export`（复用 list 基础设施 + 文件输出）
- [ ] 实现 `wr config` 管理命令（init / set / show）
- [ ] 实现 `wr update` 和 `wr cancel`
- [ ] 存储 CRUD 并发安全（`sync.Mutex`）
- [ ] Short ID 生成（SHA-256 + 单调计数器防碰撞）
- [ ] 记录完成/取消时的目录迁移

**交付标准**：支持批量导入 JSONL 文件，单条失败不影响已通过的记录；export 输出与 list 格式一致。

### Phase 4：高级特性与 Agent 命名空间 (Week 4+)

**目标**：规范完全对齐，dry-run 支持。

- [ ] 实现 `--dry-run` 支持（对写操作）
- [ ] 实现 `agent_*` 保留命名空间（如需要：7 个核心 Required + 10 个可选 Optional）
- [ ] LLM 集成（文本/图片分类）
- [ ] 定时提醒调度器
- [ ] 崩溃现场 `.last-crash.json` dump
- [ ] 自动化测试覆盖率 > 80%

**交付标准**：7 个核心（Required）`agent xxx` 命令可用（如需要），`--dry-run` 对所有写操作生效。10 个可选（Optional）命令按需实现。

---

## 四、Thin Client + Daemon 架构模式

### 4.1 为什么需要 Daemon

wr 的 CLI 命令（如 `wr list`）不直接操作文件系统或调用 LLM。它们通过 HTTP 调用本地 daemon，由 daemon 持有业务状态和连接。

```
Thin CLI 进程（每次调用新建，秒级生命周期）
    │
    │ HTTP GET http://127.0.0.1:17530/api/list?type=meeting
    │
    ▼
Daemon 进程（常驻，持有 Storage/LLM/Scheduler 实例）
```

### 4.2 职责分离

| 层 | 职责 | 不做什么 |
|---|---|---|
| **CLI (cmd/)** | 参数解析、构建 HTTP 请求、透传 daemon 响应、提取退出码 | 不读文件、不调 LLM、不持有状态 |
| **Client (internal/client/)** | 读 daemon 状态文件、发 HTTP 请求、验证响应格式、错误码映射 | 不包含业务逻辑 |
| **Daemon (internal/daemon/)** | 路由分发、业务 handler、LLM 调用、存储操作、定时调度 | 不知道 CLI 是谁 |
| **JSONL (internal/jsonl/)** | 信封构造、格式验证、写入 io.Writer | 不知道业务语义 |

### 4.3 Client 实现细节

CLI 通过读 daemon 状态文件发现 daemon 的端口：

```go
// internal/client/client.go
func CallDaemon(w io.Writer, method, path string, body io.Reader) error {
    // 1. 读状态文件获取 port
    dir, err := daemon.DefaultStateDir()
    state, err := daemon.ReadState(dir)

    // 2. 发 HTTP 请求
    url := fmt.Sprintf("http://127.0.0.1:%d%s", state.Port, path)
    httpClient := &http.Client{Timeout: clientTimeout} // 5s
    resp, err := httpClient.Do(req)

    // 3. 验证响应是合法 JSONL
    var record map[string]interface{}
    json.Unmarshal(bytes.TrimSpace(respBody), &record)

    // 4. 透传响应到 stdout
    fmt.Fprintf(w, "%s", respBody)

    // 5. 如果是错误信封，返回 ExitError
    if record["type"] == "error" {
        return &exitcode.ExitError{
            Code: exitcode.FromErrorCode(record["error_code"].(string)),
            Err:  errors.New(record["message"].(string)),
        }
    }
    return nil
}
```

### 4.4 优缺点分析

**优点：**

| 优点 | 说明 |
|---|---|
| 进程隔离 | CLI 崩溃不影响 daemon；daemon panic 被 middleware 捕获 |
| 连接复用 | LLM HTTP 客户端、定时调度器在 daemon 进程内长期存活 |
| 热更新 | 修改 CLI 不需要重启 daemon；更新 daemon 不影响已编译的 CLI |
| 并发安全 | Storage 的 mutex 保护所有写操作，无需进程间锁 |
| 测试友好 | 测试时用 `httptest.Server` 替代真实 daemon，零 mock 代码 |

**缺点：**

| 缺点 | 缓解措施 |
|---|---|
| 多一个进程要管理 | daemon start/stop 命令 + stale state 自动清理 |
| daemon 未启动时 CLI 报错 | 明确的 `error_code: daemon_not_running` + 修复建议 |
| 端口冲突风险 | IsPortInUse() 检查 + 错误提示 |
| 网络层引入了延迟 | 127.0.0.1 本地回环，延迟 < 1ms，可忽略 |

> **wr 实践注记**：这是 wr 最重要的架构决策。初期曾考虑过"CLI 直接操作文件"的简单方案，但 LLM 调用需要 API key 管理、HTTP client 复用、定时调度——这些在每次 CLI 调用时重新初始化的开销不可接受。Daemon 模式让这些资源只初始化一次。

---

## 五、错误处理与退出码传播

### 5.1 ExitError — 实现 error 接口的退出码载体

```go
// internal/exitcode/exitcode.go
type ExitError struct {
    Code int
    Err  error
}

func (e *ExitError) Error() string {
    if e.Err != nil {
        return fmt.Sprintf("exit %d: %v", e.Code, e.Err)
    }
    return fmt.Sprintf("exit %d", e.Code)
}

func (e *ExitError) Unwrap() error {
    return e.Err
}
```

### 5.2 Execute() 通过 errors.As 提取退出码

`os.Exit()` 只出现在 `main.go`——这是 Go 中保证 defer 执行的唯一方式：

```go
// cmd/root.go
func Execute() (code int) {
    defer func() {
        if r := recover(); r != nil {
            jsonl.DefaultWriter.ErrorWithCode("FATAL_CRASH",
                fmt.Sprintf("panic: %v", r))
            code = exitcode.ExitFatalError
        }
    }()
    if err := rootCmd.Execute(); err != nil {
        var exitErr *exitcode.ExitError
        if errors.As(err, &exitErr) {
            return exitErr.Code  // ← 提取语义退出码
        }
        return exitcode.ExitFatalError  // ← 未预期的 error → exit 1
    }
    return exitcode.ExitSuccess
}

// main.go
func main() {
    os.Exit(cmd.Execute())
}
```

### 5.3 退出码语义映射

```go
// internal/exitcode/exitcode.go
const (
    ExitSuccess           = 0  // 成功
    ExitFatalError        = 1  // 致命错误（存储故障、marshal 失败等）
    ExitInvalidParams     = 2  // 参数无效（bad type、body、field）
    ExitDaemonUnreachable = 3  // daemon 未运行或不可达
    ExitNetworkError      = 4  // 网络/LLM 错误
    ExitLockConflict      = 5  // 并发锁冲突
)
```

Daemon 的 `error_code`（字符串）到 CLI 的 exit code（整数）映射：

```go
func FromErrorCode(code string) int {
    switch code {
    case "invalid_type", "invalid_body", "invalid_field", "method_not_allowed":
        return ExitInvalidParams          // → 2
    case "daemon_not_running":
        return ExitDaemonUnreachable       // → 3
    case "llm_error", "llm_not_configured":
        return ExitNetworkError            // → 4
    case "lock_conflict":
        return ExitLockConflict            // → 5
    case "FATAL_CRASH":
        return ExitFatalError              // → 1
    default:
        return ExitFatalError              // → 1 (保守兜底)
    }
}
```

### 5.4 ErrorCode: String vs Int 的取舍

| 方案 | 优点 | 缺点 |
|---|---|---|
| **String（wr 选择）** | 可读性强，Agent 可直接用于错误消息展示；JSONL 中自解释 | 占用稍多字节 |
| **Int** | 紧凑，适合有限的退出码集合 | 需要额外映射表；JSONL 中不直观 |

wr 的双层设计解决了这个矛盾：
- **JSONL 内部**：使用 string `error_code`（如 `"invalid_type"`），方便 Agent 解析和展示
- **OS 层面**：使用 int exit code（如 `2`），方便 shell 脚本和 CI 判断

```jsonl
// Agent 看到的 JSONL
{"version":"1.0","tool":"wr","type":"error","timestamp":"...","error_code":"invalid_type","message":"invalid type: \"bad\" (must be meeting, task, reminder, or log)"}
```

```bash
# Shell 看到的退出码
$ wr add --type bad --title "test" --date 2026-01-01; echo $?
2
```

> **wr 实践注记**：`FromErrorCode` 的 default 分支返回 `ExitFatalError`（1）而非 0。这是保守策略——未知的 error_code 永远不应该被视为成功。新增加 error_code 时不需要同时修改映射表，但需要评估是否应该映射到更具体的退出码。

---

## 六、数据导入导出模式

### 6.1 Fail-Fast Import（两阶段验证）

批量导入时不应该"导入一半然后报错"——这是数据损坏的源头。正确的模式是两阶段：

```
阶段 1：验证 — 读取所有记录，校验每条，收集错误
    ├── 全部通过 → 进入阶段 2
    └── 有失败 → 输出所有错误，exit 2，不写入任何数据

阶段 2：执行 — 逐条写入，记录成功/失败
    └── 输出每条记录的结果，最终汇总
```

wr 目前没有实现 `wr import`，但存储层已经为批量操作做好了准备：

```go
// internal/storage/storage.go — 写操作已加锁
type Storage struct {
    baseDir string
    logger  *log.Logger
    mu      sync.Mutex      // 所有写操作序列化
    seq     uint64          // 单调计数器，保证 ShortID 唯一
}

func (s *Storage) AddRecord(rec interface{}) (interface{}, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    // ... 生成 ShortID + 写入文件 ...
}
```

如果实现 import，handler 模式应该是：

```go
// 假设的 import handler
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
    records, err := parseImportBody(r.Body)
    if err != nil {
        writeEnvelope(w, jsonl.ErrorEnvelope("invalid_body", err.Error()))
        return
    }

    // 阶段 1：验证全部
    var validationErrors []map[string]interface{}
    for i, rec := range records {
        if !models.IsValidType(rec.Type) {
            validationErrors = append(validationErrors, map[string]interface{}{
                "index": i, "error": fmt.Sprintf("invalid type: %q", rec.Type),
            })
        }
        if rec.Title == "" {
            validationErrors = append(validationErrors, map[string]interface{}{
                "index": i, "error": "missing title",
            })
        }
    }
    if len(validationErrors) > 0 {
        writeEnvelope(w, jsonl.ErrorEnvelope("validation_failed",
            fmt.Sprintf("%d records have validation errors", len(validationErrors))))
        return
    }

    // 阶段 2：执行写入
    var imported, failed int
    for _, rec := range records {
        _, err := s.storage.AddRecord(buildRecord(rec))
        if err != nil {
            failed++
        } else {
            imported++
        }
    }
    writeEnvelope(w, jsonl.SuccessEnvelope(map[string]interface{}{
        "imported": imported, "failed": failed,
    }))
}
```

### 6.2 Filtered Export（复用 list 基础设施）

导出是 list 的文件输出变体——复用 `ListOptions` 过滤机制，写入文件而非 stdout：

```go
// 假设的 export handler
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
    opts := storage.ListOptions{
        RecordType: models.RecordType(query.Get("type")),
        DateFrom:   query.Get("from"),
        DateTo:     query.Get("to"),
        Status:     query.Get("status"),
    }

    records, err := s.storage.ListRecords(opts)
    // ... 与 handleList 完全相同的过滤逻辑 ...
}
```

### 6.3 Bulk Timeout

批量操作的 HTTP 请求需要比单条操作更长的超时。wr 的 client 目前使用固定 5s 超时：

```go
const clientTimeout = 5 * time.Second
```

如果引入 import/export，应该根据操作类型动态设置超时：

```go
func getTimeout(method, path string) time.Duration {
    if strings.Contains(path, "/import") || strings.Contains(path, "/export") {
        return 60 * time.Second  // 批量操作给 60s
    }
    return 5 * time.Second       // 单条操作 5s
}
```

### 6.4 Buffer Capture for File Output

JSONL 默认写到 stdout（`os.Stdout`）。需要文件输出时，wr 的 `jsonl.Writer` 通过 `io.Writer` 接口支持任意目标：

```go
// 写入文件
f, _ := os.Create("export.jsonl")
w := jsonl.NewWriter(f)
w.Success(record)

// 写入内存 buffer（测试用）
var buf bytes.Buffer
w := jsonl.NewWriter(&buf)
w.Success(record)
// buf.String() 就是 JSONL 输出
```

测试中利用这个特性做输出捕获和断言（见第七章）。

> **wr 实践注记**：import/export 是 wr v0.2 的规划功能。当前版本通过 `wr list --type meeting --from 2026-01-01` 获取 JSONL 输出，Agent 可以 pipe 到文件。但原生 export 更可靠（能保证文件完整写入，不会被 stdout 截断）。

---

## 七、测试策略与 CI 保障

### 7.1 内联 JSON Fixture vs External File

wr 使用**内联 JSON 字符串**作为测试 fixture，而非外部文件：

```go
// cmd/exitcode_test.go — 模拟 daemon 返回错误信封
ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    env := jsonl.ErrorEnvelope("invalid_type", "missing required field: type")
    b, _ := json.Marshal(env)
    w.Header().Set("Content-Type", "application/jsonl")
    fmt.Fprintf(w, "%s\n", b)
}))
```

```go
// internal/client/client_test.go — 模拟 daemon POST 请求
err := callDaemonWithDir(&buf, dir, http.MethodPost, "/api/add",
    strings.NewReader(`{"type":"meeting","title":"sync","date":"2026-05-02"}`))
```

**选择理由**：
- fixture 紧贴测试代码，修改时不需要跨文件跳转
- `httptest.Server` + 内联 handler 比文件加载更快（无 I/O）
- wr 的 JSON 结构简单（一个信封），不需要复杂的 fixture 文件

**何时用外部文件**：如果 JSON 结构超过 20 行（如完整的 report 结构），提取为 `testdata/*.json` 更清晰。

### 7.2 Windows 时钟分辨率 + 单调计数器

Windows 的 `time.Now()` 精度只有 ~100ns 到 1ms。如果两条记录在同一毫秒内创建，仅靠时间戳生成 ShortID 会碰撞。wr 用单调计数器解决：

```go
// internal/models/record.go
func ShortIDFromTimestampAndSeq(t time.Time, seq uint64) string {
    input := fmt.Sprintf("%s-%d", t.Format("20060102_150405.999999999"), seq)
    h := sha256.Sum256([]byte(input))
    return fmt.Sprintf("%x", h[:8]) // 16 字符 hex
}
```

```go
// internal/storage/storage.go
func (s *Storage) AddRecord(rec interface{}) (interface{}, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    // ...
    s.seq++    // ← mutex 保护下的单调递增
    shortID := models.ShortIDFromTimestampAndSeq(now, s.seq)
    cf.ShortID = shortID
    // ...
}
```

**为什么不用 UUID**：ShortID 是给人看的（`wr update 2a0f2b1c`），16 字符 hex 比 36 字符 UUID 友好得多。碰撞概率在 seq 计数器的保护下为零。

### 7.3 ValidateEnvelope 共享 Helper

所有涉及 JSONL 输出的测试都使用 `validateAllEnvelopes` 或 `validateJSONLOutput`：

```go
// cmd/exitcode_test.go
func validateAllEnvelopes(t *testing.T, data []byte) {
    t.Helper()
    for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
        if line == "" { continue }
        var env jsonl.Envelope
        json.Unmarshal([]byte(line), &env)
        if err := jsonl.ValidateEnvelope(env); err != nil {
            t.Errorf("envelope validation failed: %v; line=%s", err, line)
        }
    }
}

// 使用
code, out := executeCmd("add", "--type", "task", "--title", "test", "--date", "2024-01-01")
validateAllEnvelopes(t, out)  // 每个测试都跑
```

```go
// internal/jsonl/output_test.go — 更细粒度的断言
func assertEnvelopeMeta(t *testing.T, env map[string]interface{}, expectedType string) {
    t.Helper()
    if env["version"] != "1.0" { t.Errorf(...) }
    if env["tool"] != "wr" { t.Errorf(...) }
    if env["type"] != expectedType { t.Errorf(...) }
    ts, _ := env["timestamp"].(string)
    if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
        t.Errorf("timestamp not RFC3339Nano: %v", ts)
    }
}
```

### 7.4 测试环境隔离

wr 的测试使用 `setupTempHome` 将 daemon 状态文件重定向到临时目录：

```go
// cmd/exitcode_test.go
func setupTempHome(t *testing.T) (string, func()) {
    tmpHome := t.TempDir()
    origHome := os.Getenv("HOME")
    os.Setenv("HOME", tmpHome)
    os.Setenv("USERPROFILE", tmpHome)  // Windows 兼容
    return tmpHome, func() {
        os.Setenv("HOME", origHome)
        os.Setenv("USERPROFILE", origUserProfile)
    }
}
```

配合 `setupFakeDaemon` 创建指向 `httptest.Server` 的状态文件：

```go
func setupFakeDaemon(t *testing.T, tmpHome string, handler http.HandlerFunc) *httptest.Server {
    srv := httptest.NewServer(handler)
    // 将 daemon 状态文件指向测试服务器的端口
    daemon.WriteState(stateDir, daemon.DaemonState{Port: portInt, PID: os.Getpid()})
    return srv
}
```

### 7.5 Exit Code 测试矩阵

每个语义退出码都有对应的集成测试，使用 fake daemon 验证完整链路：

```go
// cmd/exitcode_test.go — 测试矩阵
func TestSuccessExit0(t *testing.T)             // exit 0
func TestFatalErrorExit1(t *testing.T)          // exit 1 — storage_error
func TestInvalidParamsExit2(t *testing.T)       // exit 2 — invalid_type
func TestDaemonUnreachableExit3(t *testing.T)   // exit 3 — daemon not running
func TestLLMErrorExit4(t *testing.T)            // exit 4 — llm_error
func TestLockConflictExit5(t *testing.T)        // exit 5 — lock_conflict
func TestDaemonNotRunningNoStateExit3(t *testing.T) // exit 3 — no state file
```

> **wr 实践注记**：测试中使用 `t.TempDir()` 而非 `ioutil.TempDir`——前者在测试结束时自动清理，避免 CI 磁盘泄漏。JSONL 输出的时间戳通过 `nowFunc` 可覆盖（`jsonl` 包的 `var nowFunc`），但当前测试没有利用这一点——直接断言 timestamp 非空且格式正确更简单。

### 7.6 AST 级别的 JSONL 纯度 Linter

规范 5.3 节要求 CI 中运行 Linter 检测非 JSONL 的 `print()` 调用。下面给出一个跨语言的实现模式。

#### 7.6.1 设计原则

1. **AST 级别检测**：正则表达式容易误报（注释中的 `print`、字符串中的 `print`）。AST 解析可以精确定位函数调用节点。
2. **白名单排除**：SDK 内部的 `jsonl_emit()` 函数需要调用底层 print，测试文件中的 `print()` 也不应被标记。维护排除目录和排除函数列表。
3. **零误报容忍**：宁可漏报（新增 SDK 函数未加入白名单）也不能误报（阻断正常开发流程）。

#### 7.6.2 Python 实现示例

```python
# scripts/check_jsonl_purity.py
import ast
import sys
from pathlib import Path

ALLOWED_DIRS = {"tests", "scripts"}          # 排除的目录
ALLOWED_FUNCTIONS = {"jsonl_emit", "jsonl_emit_error", "jsonl_emit_result"}
SOURCE_DIR = Path("src")

def check_file(filepath: Path) -> list[str]:
    """检查单个 Python 文件中的非白名单 print() 调用。"""
    violations = []
    tree = ast.parse(filepath.read_text(encoding="utf-8"))
    for node in ast.walk(tree):
        if isinstance(node, ast.Call):
            func_name = None
            if isinstance(node.func, ast.Name):
                func_name = node.func.id
            elif isinstance(node.func, ast.Attribute):
                func_name = node.func.attr
            if func_name == "print":
                violations.append(f"{filepath}:{node.lineno}")
    return violations

def main():
    violations = []
    for f in SOURCE_DIR.rglob("*.py"):
        if any(d in f.parts for d in ALLOWED_DIRS):
            continue
        violations.extend(check_file(f))
    if violations:
        for v in violations:
            print(f"VIOLATION: {v}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
```

#### 7.6.3 测试 Linter 本身

Linter 本身也需要测试——覆盖各种边界情况：

- 普通 `print()` → 应被检测
- f-string `print(f"val={x}")` → 应被检测
- `# print("commented")` → 不应被检测（注释不是 AST 节点）
- 字符串中的 `"print("` → 不应被检测
- `jsonl_emit(data)` → 不应被检测
- 测试文件中的 `print()` → 不应被检测（目录排除）

> **web-clip-helper 实践注记**：`scripts/check_jsonl_purity.py` 的 22 个测试覆盖了上述所有场景。Linter 在 CI 中作为独立 job 运行，任何 `src/` 目录下的非法 `print()` 调用都会阻断合并。Linter 检测到的违规数量应随项目成熟度趋近于零——如果频繁触发，说明 SDK 的输出 API 不够便利，开发者才绕过它。

---

## 附录 A：wr 包结构索引

```
wr/
├── main.go                        # 入口：7 行
├── cmd/                           # Thin CLI 层
│   ├── root.go                    # Execute() + panic 恢复 + ExitError 提取
│   ├── add.go                     # wr add — 参数构建 + HTTP POST
│   ├── list.go                    # wr list — 查询参数拼接到 URL
│   ├── report.go                  # wr report — today/date/week/range/push
│   ├── config.go                  # wr config — init/set/show
│   ├── daemon.go                  # wr daemon start/stop
│   ├── complete.go                # wr complete <id>
│   ├── cancel.go                  # wr cancel <id>
│   ├── update.go                  # wr update <id> --field value
│   ├── status.go                  # wr status
│   └── *_test.go                  # 集成测试（httptest + fake daemon）
├── internal/
│   ├── jsonl/output.go            # JSONL 信封 + Writer
│   ├── exitcode/exitcode.go       # ExitError + FromErrorCode 映射
│   ├── sandbox/sandbox.go         # 目录创建 + daemon.log
│   ├── daemon/
│   │   ├── server.go              # HTTP server + 路由 + middleware
│   │   ├── handler.go             # 业务 handlers（add/list/update/report...）
│   │   └── state.go               # .daemon.json 读写 + 备份恢复
│   ├── client/client.go           # HTTP 客户端 + 错误码映射
│   ├── storage/storage.go         # 文件 CRUD + 目录结构 + 并发锁
│   ├── models/record.go           # 数据结构 + ShortID 生成 + JSON 序列化
│   ├── config/config.go           # 配置加载/验证/脱敏
│   ├── llm/
│   │   ├── classify.go            # 文本分类（LLM API 调用）
│   │   ├── client.go              # LLM HTTP 客户端
│   │   └── vision.go              # 图片分类
│   ├── report/report.go           # 日报/周报生成
│   ├── pushover/pushover.go       # Pushover 通知发送
│   └── scheduler/                 # 定时提醒调度器
│       ├── scheduler.go           # 调度引擎
│       ├── state.go               # 调度状态持久化
│       └── catchup.go             # 重启后补发遗漏提醒
```

## 附录 B：JSONL 输出示例

```jsonl
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-04T03:15:00Z","data":{"short_id":"2a0f2b1c","type":"meeting","title":"团队周会","date":"2026-05-04","time":"14:00","status":"active"}}
```

```jsonl
{"version":"1.0","tool":"wr","type":"error","timestamp":"2026-05-04T03:15:01Z","error_code":"invalid_type","message":"invalid type: \"bad\" (must be meeting, task, reminder, or log)"}
```

```jsonl
{"version":"1.0","tool":"wr","type":"error","timestamp":"2026-05-04T03:15:02Z","error_code":"daemon_not_running","message":"daemon not running: state file and backup both unreadable. Run 'wr daemon start' to start the daemon, then retry your command."}
```

```jsonl
{"version":"1.0","tool":"wr","type":"warning","timestamp":"2026-05-04T03:15:03Z","message":"daemon shutting down"}
```

```jsonl
{"version":"1.0","tool":"wr","type":"progress","timestamp":"2026-05-04T03:15:04Z","percent":50,"message":"importing records..."}
```

## 附录 C：错误码速查表

| error_code | 退出码 | 触发场景 |
|---|---|---|
| — | 0 | 成功 |
| `FATAL_CRASH` | 1 | panic 恢复、marshal 失败 |
| `storage_error` | 1 | 文件读写失败、记录未找到（默认兜底） |
| `record_not_found` | 1 | update/complete/cancel 指定了不存在的 ID |
| `invalid_type` | 2 | type 字段不在 meeting/task/reminder/log 中 |
| `invalid_body` | 2 | JSON 解析失败、缺少必填字段 |
| `invalid_field` | 2 | update 包含不允许修改的字段 |
| `method_not_allowed` | 2 | HTTP 方法不匹配 |
| `daemon_not_running` | 3 | 状态文件不存在/损坏/daemon 不可达 |
| `llm_error` | 4 | LLM API 调用失败 |
| `llm_not_configured` | 4 | LLM API key 未配置 |
| `lock_conflict` | 5 | 并发写冲突（预留） |
| `push_error` | 1 | Pushover 发送失败 |
| `pushover_not_configured` | 1 | Pushover 凭证未配置 |
| `already_completed` | 1 | 对已完成的记录执行 complete |
| `already_cancelled` | 1 | 对已取消的记录执行 cancel |
| `marshal_error` | 1 | daemon 响应序列化失败 |
