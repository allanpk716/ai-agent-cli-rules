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
- **Agent 元命令** — 内置 `agent schema`（自描述命令树）、`agent errors`（错误码表）、`agent config`（配置读写）、`agent health`（健康检查）
- **脱敏红线** — 配置中标记为 sensitive 的字段，输出时自动替换为 `[REDACTED]`

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
pip install "agentsdk @ git+https://github.com/allanpk716/ai-agent-cli-rules.git@v0.2.0#subdirectory=sdks/python"
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
agentsdk @ git+https://github.com/allanpk716/ai-agent-cli-rules.git@v0.2.0#subdirectory=sdks/python
```

```toml
# pyproject.toml — 在 [project.dependencies] 中
[project]
dependencies = [
    "agentsdk @ git+https://github.com/allanpk716/ai-agent-cli-rules.git@v0.2.0#subdirectory=sdks/python",
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
│   │   └── ..._test.go        # 每个模块对应测试
│   └── python/                # Python SDK (Typer-based)
│       └── agentsdk/
│           ├── app.py         # App 入口，含 stdout 劫持
│           ├── envelope.py    # JSONL Envelope 协议类型
│           ├── writer.py      # JSONL 输出 Writer
│           ├── config.py      # ConfigManager (Pydantic)
│           ├── sandbox.py     # 跨平台目录管理
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
