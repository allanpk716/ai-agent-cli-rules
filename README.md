# Agent CLI SDK

一套用于构建 **AI Agent 原生 CLI 工具** 的 SDK，提供 Go 和 Python 双语言实现。

CLI 工具通常是为人类设计的——带颜色、进度条、交互式提示。但当 AI Agent 成为 CLI 的主要用户时，这些"人性化"设计反而变成了噪声。Agent 需要 100% 机器可读的输出、确定性的行为、自我描述的能力。

本 SDK 基于设计规范 [《面向 AI Agent 的 CLI 工具设计规范》](docs/面向%20AI%20Agent%20的%20CLI%20工具设计规范/面向%20AI%20Agent%20的%20CLI%20工具设计规范.md) 和 [《工程落地与 SDK 架构指南》](docs/面向%20AI%20Agent%20的%20CLI%20工具设计规范/Agent-CLI%20工程落地与%20SDK%20架构指南%20\(Developer%20Playbook\).md)，将规范中的核心约束落地为可直接使用的代码。

## 核心特性

- **JSONL 协议** — 所有 stdout 输出均为 JSON Lines 格式，带统一 Envelope 结构（version / tool / type / timestamp / data）
- **字段互斥** — `data`、`error_code`、`percent` 三个字段由 `type` 决定，构造函数隔离，类型系统保证不会混用
- **静默模式 (`--quiet`)** — 屏蔽 progress 输出，只保留终态 result/error，适合 Agent 编排
- **崩溃恢复** — panic/go-runtime-error 自动捕获，输出 FATAL_CRASH envelope 而非裸堆栈
- **信号处理** — SIGINT/SIGTERM 优雅退出，可选飞行记录（FlightContext）用于事后诊断
- **沙箱目录** — 单目录模式管理配置、数据、缓存，自动跨平台适配
- **Agent 元命令** — 内置 `agent schema`（自描述命令树）、`agent errors`（错误码表）、`agent config`（配置读写）、`agent health`（健康检查）、`agent doctor`（诊断检查）、`agent debug`（调试信息）、`agent cache`（缓存管理）
- **脱敏红线** — 配置中标记为 sensitive 的字段，输出时自动替换为 `[REDACTED]`
- **结构化日志** — 独立 Logger 实例，支持 WithField/WithFields 结构化字段、时间/大小双轮转策略、自动过期清理、优雅降级到 stderr

## 快速开始

### Go SDK

```bash
go get github.com/allanpk716/agent-cli-sdk
```

```go
package main

import (
    "fmt"
    "os"

    agentsdk "github.com/allanpk716/agent-cli-sdk"
    "github.com/spf13/cobra"
)

type myConfig struct {
    Name   string `json:"name"   config:"true"`
    APIKey string `json:"api_key" sensitive:"true"`
}

func main() {
    app := agentsdk.New("my-tool", "1.0.0")

    // 配置管理
    cfgMgr := agentsdk.NewConfigManager[myConfig](app.Sandbox().DataDir() + "/config.json")
    app.RegisterConfig("default", cfgMgr)

    // 业务命令
    rootCmd := &cobra.Command{Use: "my-tool"}
    rootCmd.AddCommand(&cobra.Command{
        Use: "hello",
        RunE: func(cmd *cobra.Command, args []string) error {
            return app.JSONL().Success(map[string]string{
                "message": "Hello, Agent!",
            })
        },
    })

    // 注册 agent 元命令（schema / errors / config / health）
    rootCmd.AddCommand(app.AgentCommands())

    os.Exit(app.Execute(rootCmd))
}
```

运行：

```bash
$ my-tool hello
{"version":"1.0","tool":"my-tool","type":"result","timestamp":"2026-05-05T12:00:00Z","data":{"message":"Hello, Agent!"}}

$ my-tool agent schema
{"version":"1.0","tool":"my-tool","type":"result","timestamp":"...","data":{"commands":[...]}}
```

### Python SDK

**方式一：从 PyPI 安装（推荐）**

```bash
pip install agentsdk
```

> 需要 PyPI 已发布对应版本。查看 `sdks/python/pyproject.toml` 中的 `version` 字段确认当前版本。

**方式二：从 GitHub 仓库安装**

```bash
# 安装最新 main 分支
pip install "agentsdk @ git+https://github.com/allanpk716/ai-agent-cli-rules.git#subdirectory=sdks/python"

# 安装指定 tag
pip install "agentsdk @ git+https://github.com/allanpk716/ai-agent-cli-rules.git@v0.4.0#subdirectory=sdks/python"
```

**方式三：本地 editable 安装（开发时使用）**

将本项目克隆到本地后：

```bash
# 克隆仓库（如果还没有）
git clone https://github.com/allanpk716/ai-agent-cli-rules.git
cd ai-agent-cli-rules

# editable 安装，代码修改即时生效
pip install -e "./sdks/python[dev]"
```

**方式四：requirements.txt / pyproject.toml 中声明依赖**

```txt
# requirements.txt — 指定 tag 版本
agentsdk @ git+https://github.com/allanpk716/ai-agent-cli-rules.git@v0.4.0#subdirectory=sdks/python
```

```toml
# pyproject.toml — 在 [project.dependencies] 中
[project]
dependencies = [
    "agentsdk @ git+https://github.com/allanpk716/ai-agent-cli-rules.git@v0.4.0#subdirectory=sdks/python",
]
```

```python
from agentsdk import App, ConfigManager, ExitError, EXIT_INVALID_PARAMS
import typer
from pydantic import BaseModel, Field

class MyConfig(BaseModel):
    name: str = Field(default="world", json_schema_extra={"config": True})
    api_key: str = Field(default="", json_schema_extra={"sensitive": True})

def main():
    app = App("my-tool", "1.0.0")

    cfg_mgr = ConfigManager[MyConfig](MyConfig, f"{app.sandbox.data_dir}/config.json")
    app.register_config("default", cfg_mgr)

    cli = typer.Typer(name="my-tool", no_args_is_help=True)

    @cli.command()
    def hello():
        app.writer.success({"message": "Hello, Agent!"})

    @cli.command()
    def fail():
        app.writer.error_with_code("INPUT_INVALID", "something went wrong")
        raise ExitError(EXIT_INVALID_PARAMS, "something went wrong")

    cli.add_typer(app.agent_commands(), name="agent")
    app.run(cli)

if __name__ == "__main__":
    main()
```

运行：

```bash
$ python main.py hello
{"version":"1.0","tool":"my-tool","type":"result","timestamp":"2026-05-05T12:00:00Z","data":{"message":"Hello, Agent!"}}

$ python main.py agent schema
{"version":"1.0","tool":"my-tool","type":"result","timestamp":"...","data":{"commands":[...]}}
```

## JSONL 输出示例

每个输出都是一个 Envelope：

```jsonl
{"version":"1.0","tool":"hello-agent","type":"result","timestamp":"2026-05-05T10:00:00Z","data":{"greeting":"Hello, world!"}}
{"version":"1.0","tool":"hello-agent","type":"progress","timestamp":"2026-05-05T10:00:01Z","percent":50,"message":"halfway there"}
{"version":"1.0","tool":"hello-agent","type":"error","timestamp":"2026-05-05T10:00:02Z","error_code":"INPUT_INVALID","message":"name is required"}
{"version":"1.0","tool":"hello-agent","type":"warning","timestamp":"2026-05-05T10:00:03Z","message":"something seems off"}
```

字段互斥规则：

| type | 允许字段 | 禁止字段 |
|------|----------|----------|
| `result` | `data` (必填) | `error_code`, `percent` |
| `error` | `error_code` (必填), `message` (必填) | `data`, `percent` |
| `warning` | `message` (必填) | `data`, `error_code`, `percent` |
| `progress` | `percent` (必填) | `data`, `error_code` |

## 结构化日志

SDK 提供了独立的结构化 Logger，支持 `WithField`/`WithFields` 结构化字段输出，自动文件轮转和过期清理。

> 每个 Logger 实例完全独立，无全局状态，多个 Logger 可以共存互不干扰。当日志目录不可写时，Logger 自动降级到 stderr-only 输出。

### API 说明

#### Go

```go
// LoggerSettings 配置 Logger 实例
type LoggerSettings struct {
    Level               logrus.Level // 最低日志级别，默认 Info
    LogDir              string       // 日志文件目录
    LogNameBase         string       // 日志文件名前缀，默认为 appName
    RotationTime        time.Duration // 时间轮转间隔，默认 24h
    MaxAgeDays          int          // 最大保留天数，默认 7
    MaxSizeMB           int          // 大小轮转阈值（>0 启用），默认 0（使用时间轮转）
    UseHierarchicalPath bool         // 使用 YYYY/MM/DD 目录结构
}

// NewLogger 创建独立 Logger 实例
func NewLogger(settings LoggerSettings) (*Logger, error)

// Logger 方法
func (l *Logger) Debug(args ...interface{})
func (l *Logger) Info(args ...interface{})
func (l *Logger) Warn(args ...interface{})
func (l *Logger) Error(args ...interface{})
func (l *Logger) WithField(key string, value interface{}) *logrus.Entry
func (l *Logger) WithFields(fields map[string]interface{}) *logrus.Entry
func (l *Logger) Close() error
```

#### Python

```python
@dataclass
class LoggerSettings:
    level: int = logging.INFO          # 最低日志级别
    log_dir: str = ""                  # 日志文件目录
    log_name_base: str = ""            # 日志文件名前缀
    rotation_time_hours: int = 24      # 时间轮转间隔（小时）
    max_age_days: int = 7              # 最大保留天数
    max_size_mb: int = 0               # 大小轮转阈值（>0 启用）

class Logger:
    def debug(self, msg: str) -> None: ...
    def info(self, msg: str) -> None: ...
    def warning(self, msg: str) -> None: ...
    def error(self, msg: str) -> None: ...
    def with_field(self, key: str, value: Any) -> logging.Logger: ...
    def with_fields(self, fields: Dict[str, Any]) -> logging.Logger: ...
    def close(self) -> None: ...
```

### 使用示例

#### Go — 通过 App 集成

```go
app := agentsdk.New("my-tool", "1.0.0")
// app.Logger() 自动创建，日志写入 sandbox.LogsDir()
// 在 App.Execute() 中自动 defer Close()

app.Logger().Info("starting processing")
app.Logger().WithField("user_id", 42).Info("user login")
app.Logger().WithFields(logrus.Fields{"file": "data.csv", "rows": 100}).Info("file loaded")
```

#### Go — 独立使用

```go
settings := agentsdk.DefaultLoggerSettings("my-app", "/var/log/my-app")
settings.MaxSizeMB = 100 // 切换到大小轮转
logger, err := agentsdk.NewLogger(settings)
if err != nil { log.Fatal(err) }
defer logger.Close()

logger.Info("server started")
```

#### Python — 通过 App 集成

```python
app = App("my-tool", "1.0.0")
# app.logger 自动创建，日志写入 sandbox.logs_dir
# 在 App.run() 中自动关闭

app.logger.info("starting processing")
app.logger.with_field("user_id", 42).info("user login")
app.logger.with_fields({"file": "data.csv", "rows": 100}).info("file loaded")
```

#### Python — 独立使用

```python
from agentsdk import Logger, default_logger_settings

settings = default_logger_settings("my-app", "/var/log/my-app")
settings.max_size_mb = 100  # 切换到大小轮转
logger = Logger(settings)

logger.info("server started")
logger.close()
```

### 轮转策略

| 策略 | 触发条件 | 配置字段 |
|------|----------|----------|
| 时间轮转 | 每 N 小时创建新文件（默认 24h） | `RotationTime`（Go）/ `rotation_time_hours`（Python） |
| 大小轮转 | 单文件超过 N MB 时轮转 | `MaxSizeMB`（Go）/ `max_size_mb`（Python） |
| 过期清理 | 自动删除超过 N 天的旧日志 | `MaxAgeDays`（Go）/ `max_age_days`（Python） |

> 默认使用时间轮转（`MaxSizeMB = 0`）。设置 `MaxSizeMB > 0` 后切换到大小轮转。

## 备份与还原

SDK 提供了完整的备份与还原能力，支持将沙箱目录打包为 zip 归档，以及从归档中恢复文件。还原操作默认会**覆盖**目标目录中的同名文件，请务必遵循「先备份再还原」原则。

> ⚠️ **先备份再还原**：`RestoreBackup` 会直接覆盖 `targetDir` 中已存在的同名文件。在执行还原之前，务必先对当前状态调用 `CreateBackup` 创建备份，以便在还原结果不符合预期时回退。

### API 说明

#### Go

```go
// RestoreResult 还原操作的结果
type RestoreResult struct {
    Restored []string // 成功还原的文件路径（相对于 targetDir）
    Skipped  []string // 被跳过的 zip 条目路径（如不在 items 过滤列表中）
}

// RestoreBackup 从 zip 归档还原文件到 targetDir
//   - zipPath:    zip 归档路径
//   - targetDir:  还原目标目录（不存在则自动创建）
//   - items:      可选条目过滤列表。nil 或空表示全量还原；
//                 非空时仅还原精确匹配或目录前缀匹配的条目
func RestoreBackup(zipPath, targetDir string, items []string) (RestoreResult, error)
```

#### Python

```python
@dataclass
class RestoreResult:
    restored: List[str]  # 成功还原的文件路径（相对于 target_dir）
    skipped: List[str]   # 被跳过的 zip 条目路径

def RestoreBackup(
    zip_path: str,
    target_dir: str,
    items: Optional[List[str]] = None,
) -> RestoreResult: ...
```

### 使用示例

#### Go — 全量还原

```go
result, err := agentsdk.RestoreBackup("/backups/config-v2.zip", app.Sandbox().DataDir(), nil)
if err != nil {
    app.JSONL().Error("RESTORE_FAILED", fmt.Sprintf("还原失败: %v", err))
    return err
}
fmt.Printf("还原了 %d 个文件，跳过 %d 个\n", len(result.Restored), len(result.Skipped))
```

#### Go — 选择性还原

```go
// 仅还原 config.json 和 data/ 目录下的文件
result, err := agentsdk.RestoreBackup("/backups/config-v2.zip", app.Sandbox().DataDir(), []string{
    "config.json",
    "data/",
})
if err != nil {
    // 错误处理...
}
```

#### Python — 全量还原

```python
result = RestoreBackup("/backups/config-v2.zip", app.sandbox.data_dir)
# result.restored -> ["config.json", "data/file1.txt", ...]
# result.skipped  -> []
```

#### Python — 选择性还原

```python
# 仅还原 config.json 和 data/ 目录下的文件
result = RestoreBackup(
    "/backups/config-v2.zip",
    app.sandbox.data_dir,
    items=["config.json", "data/"],
)
```

### `items` 参数用法

`items` 参数控制还原的范围：

| 值 | 行为 |
|---|---|
| `nil`（Go）/ `None`（Python） | **全量还原**：还原归档中的所有条目 |
| `[]`（空列表） | 同上，视为全量还原 |
| `["config.json"]` | **选择性还原**：仅还原精确匹配 `config.json` 的条目 |
| `["data/"]` | **选择性还原**：还原 `data/` 目录下的所有文件（目录前缀匹配） |
| `["config.json", "data/"]` | **选择性还原**：同时使用精确匹配和前缀匹配 |

匹配规则：
- **精确匹配**：条目名称与 items 中的某项完全一致（如 `"config.json"`）
- **目录前缀匹配**：items 中的某项作为目录前缀，匹配所有以此开头的条目（如 `"data/"` 匹配 `"data/file.txt"`、`"data/sub/nested.txt"`）

### 错误处理

- **首错即停**：还原过程中遇到第一个错误立即中止，已还原的文件不会回滚
- **错误前缀**：所有错误信息以 `restore:` 前缀开头，便于日志过滤和诊断
- **zip-slip 防护**：自动检测路径穿越攻击，拒绝还原到目标目录之外的路径
- **CRC-32 校验**：解压时自动验证文件完整性，损坏的归档会报错

## 项目结构

```
.
├── sdks/
│   ├── go/                    # Go SDK (Cobra-based)
│   │   ├── app.go             # App 入口，组合 Writer/Sandbox/FlightContext
│   │   ├── envelope.go        # JSONL Envelope 协议类型
│   │   ├── writer.go          # JSONL 输出 Writer
│   │   ├── config.go          # ConfigManager 泛型配置管理
│   │   ├── sandbox.go         # 跨平台目录管理
│   │   ├── crashdump.go       # 崩溃转储
│   │   ├── flightcontext.go   # 飞行记录器（黑匣子）
│   │   ├── signalhandler.go   # 信号处理
│   │   ├── exitcode.go        # 错误码注册表
│   │   ├── backup.go          # CreateBackup — 目录打包为 zip 归档
│   │   ├── restore.go         # RestoreBackup — 从 zip 归档还原文件
│   │   └── ..._test.go        # 每个模块对应测试
│   └── python/                # Python SDK (Typer-based)
│       └── agentsdk/
│           ├── app.py         # App 入口，含 stdout 劫持
│           ├── envelope.py    # JSONL Envelope 协议类型
│           ├── writer.py      # JSONL 输出 Writer
│           ├── config.py      # ConfigManager (Pydantic)
│           ├── sandbox.py     # 跨平台目录管理
│           ├── backup.py      # CreateBackup — 目录打包为 zip 归档
│           ├── restore.py     # RestoreBackup — 从 zip 归档还原文件
│           └── ...
├── examples/
│   ├── go/helloworld/         # Go 完整示例
│   └── python/helloworld/     # Python 完整示例
└── docs/
    └── 面向 AI Agent 的 CLI 工具设计规范/
        ├── 面向 AI Agent 的 CLI 工具设计规范.md      # 设计规范全文
        └── Agent-CLI 工程落地与 SDK 架构指南.md       # 工程落地指南
```

## 示例项目

两个 hello-world 示例展示了 SDK 的完整生命周期：

```bash
# Go
cd examples/go/helloworld
go run . greet Alice
go run . fail
go run . progress
go run . agent schema
go run . agent errors

# Python
cd examples/python
pip install -e ../../sdks/python
python -m examples.helloworld.main greet Alice
python -m examples.helloworld.main fail
python -m examples.helloworld.main agent schema
```

## 运行测试

```bash
# Go
cd sdks/go
go test ./...

# Python
cd sdks/python
pip install -e ".[dev]"
pytest
```

## 参考文档

| 文档 | 说明 |
|------|------|
| [设计规范 V2.3](docs/面向%20AI%20Agent%20的%20CLI%20工具设计规范/面向%20AI%20Agent%20的%20CLI%20工具设计规范.md) | 核心设计原则、JSONL 协议定义、错误码体系、配置管理、沙箱目录、数据迁移等完整规范 |
| [工程落地指南](docs/面向%20AI%20Agent%20的%20CLI%20工具设计规范/Agent-CLI%20工程落地与%20SDK%20架构指南%20\(Developer%20Playbook\).md) | 基于 wr 项目的实战经验，涵盖架构选型、存量改造策略、测试模式、陷阱与踩坑记录 |
| `examples/go/helloworld/` | Go SDK 完整示例，展示 greet/fail/progress/warn/panic + agent 元命令 |
| `examples/python/helloworld/` | Python SDK 完整示例，同上 |

## License

MIT
