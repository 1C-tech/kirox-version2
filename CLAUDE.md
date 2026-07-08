# CLAUDE.md

KiroX — AWS Builder ID 自动注册工具，基于 Wails v2 构建的 Windows/macOS/Linux 桌面应用。

## 项目概述

KiroX 自动化 Amazon Q Developer (原 CodeWhisperer) 账号注册流程，支持：
- **多邮箱源**：Outlook、MoeMail、Cloud-Mail、CF 临时邮箱
- **多代理池**：按权重随机选取代理，指纹按代理缓存
- **导出账号**：批量导出 Kiro JSON 格式（含 refreshToken、用量等完整数据）
- **订阅管理**：批量获取/修改 Kiro 订阅链接
- **自动更新**：引导浏览器下载最新版本

## 核心架构

```
┌──────────────────────────────────────────────────┐
│                    前端 (HTML/JS)                  │
│    index.html + js/*.js → build.js → dist/       │
├──────────────────────────────────────────────────┤
│               Wails 绑定层 (app.go)                │
│   App 结构体方法通过 runtime.Bind 暴露给前端        │
├──────────┬──────────┬──────────┬─────────────────┤
│ core/    │ email/   │ browser/ │ subscription/   │
│ 注册流程  │ 邮箱接口  │ 指纹生成  │ 订阅链接获取      │
│ 导出账号  │ 多实现   │ 按代理缓存 │                 │
│ 验活     │         │          │                 │
├──────────┼──────────┼──────────┼─────────────────┤
│ task/    │ data/    │ proxy/   │ updater/        │
│ 并发任务  │ 结果持久化 │ 代理池   │ 应用更新         │
│ 熔断退避  │          │         │                 │
└──────────┴──────────┴──────────┴─────────────────┘
```

### 注册流程 (core/registrar.go + core/run.go)

```
Step1 OIDC 注册 → Step2 设备授权 → Step3 获取邮箱 → Step4 Portal 初始化
→ Step5 工作流初始化 → Step6 提交邮箱 → Step7 验证邮箱 → Step8 提交密码
→ Step9 发送 OTP → Step10 创建 profile → Step11 Kiro 认证 → Step12 设置密码
→ 验活 (verify.go)
```

### 导出流程 (core/export.go)

```
ExportAccount: OIDC 刷新 token → Kiro refreshToken (获取 profileArn)
→ getUsageLimits (传入 profileArn) → 解析用量数据 → 构建导出 JSON
```

## 目录结构

```
KiroX/
├── main.go                     # 入口，Wails 桌面应用启动
├── main_windows.go             # Windows 平台选项（WebView2 设置）
├── main_darwin.go              # macOS 平台选项
├── main_linux.go               # Linux 平台选项
├── app.go                      # Wails 绑定层，所有前端可调用的 Go 方法
├── app_lang.go                 # 语言检测（Windows 注册表）
├── app_lang_other.go           # 非 Windows 语言默认
├── app_lang_windows.go         # Windows 语言检测实现
├── wails.json                  # Wails 项目配置
├── go.mod / go.sum             # Go 依赖
│
├── frontend/                   # 前端源码
│   ├── index.html              # 主页面 (单页应用)
│   ├── build.js                # Node.js 构建脚本 → dist/
│   ├── package.json            # npm 配置
│   ├── css/                    # 样式文件
│   ├── js/                     # 业务脚本
│   │   ├── app.js              # 主逻辑
│   │   ├── subscription.js     # 订阅管理 + 导出
│   │   ├── task.js             # 任务控制
│   │   ├── proxy_pool.js       # 代理池配置
│   │   ├── moemail.js          # MoeMail 配置
│   │   ├── cloudmail.js        # Cloud-Mail 配置
│   │   ├── cftempemail.js      # CF 临时邮箱配置
│   │   ├── accounts.js         # 账号管理
│   │   ├── overview.js         # 总览仪表盘
│   │   ├── ui.js               # UI 辅助
│   │   ├── i18n.js             # 国际化
│   │   └── license.js          # 许可证管理
│   ├── assets/                 # 图标/图片资源
│   ├── wailsjs/                # Wails 自动生成的 JS 绑定
│   └── dist/                   # 构建产物（嵌入到 Go binary）
│
├── internal/                   # Go 后端核心
│   ├── core/                   # 核心注册/导出逻辑
│   │   ├── config.go           # 注册配置定义
│   │   ├── registrar.go        # Registrar 结构体 + HTTP 请求封装
│   │   ├── run.go              # Run() 主流程 + 错误格式化
│   │   ├── auth.go             # OIDC 认证相关
│   │   ├── kiro_auth.go        # Kiro OAuth 流程
│   │   ├── kiro_exchange.go    # Kiro 密钥交换
│   │   ├── signup_flow.go      # AWS 注册步骤实现
│   │   ├── signup_password.go  # 密码设置步骤
│   │   ├── http_helpers.go     # HTTP 工具函数
│   │   ├── export.go           # 账号导出 + 订阅修改
│   │   └── verify.go           # 账号验活（刷新 token + 用量查询）
│   ├── email/                  # 邮箱服务抽象层
│   │   ├── interface.go        # TempEmailService 接口定义
│   │   ├── outlook_imap.go     # Outlook IMAP 邮箱
│   │   ├── moemail.go          # MoeMail 临时邮箱
│   │   ├── cloudmail.go        # Cloud-Mail 临时邮箱
│   │   ├── cftempemail.go      # CF Worker 临时邮箱
│   │   ├── manager_*.go        # 各邮箱配置管理（CRUD + 测试连接）
│   │   └── proxy_dial.go       # 代理 Dialer（IMAP 走代理）
│   ├── browser/                # 浏览器指纹模拟
│   │   ├── identity.go         # 指纹身份定义
│   │   ├── identity_cache.go   # 按代理缓存指纹
│   │   ├── fingerprint.go      # 指纹 JSON 生成
│   │   └── fp_builder.go       # 指纹字段构建
│   ├── task/                   # 任务调度
│   │   ├── coordinator.go      # 批量注册协调器（并发/串行/熔断）
│   │   └── state.go            # 任务状态管理（日志/进度/结果）
│   ├── subscription/           # 订阅链接
│   │   ├── subscription.go     # 调用 AWS API 获取订阅
│   │   └── cache.go            # 订阅链接本地缓存
│   ├── proxy/                  # 代理管理
│   │   ├── pool.go             # 多代理池（按权重随机选取）
│   │   └── detect.go           # 代理检测
│   ├── data/                   # 数据持久化
│   │   └── results.go          # 注册结果读写 (accounts.json)
│   ├── storage/                # 存储路径
│   │   └── storage.go          # 数据目录/输出目录管理
│   ├── http/                   # HTTP 客户端
│   │   └── helper.go           # TLS 客户端工厂 + Headers
│   ├── crypto/                 # 加密
│   │   ├── jwe.go              # JWE 加密（指纹加密）
│   │   └── xxtea.go            # XXTEA 加密
│   └── updater/                # 应用更新
│       ├── updater.go          # 版本检查 + 下载引导
│       ├── exec_windows.go     # Windows 浏览器启动
│       └── exec_unix.go        # Unix 浏览器启动
│
├── build/                      # 构建产物
│   └── bin/kirox.exe           # 打包后的可执行文件
│
├── docs/                       # 文档
├── README.md                   # 中文 README
├── README.en.md                # 英文 README
├── README.ja.md                # 日文 README
├── CHANGELOG.md                # 更新日志
└── CONTRIBUTING.md             # 贡献指南
```

## 开发命令

```bash
# 开发模式（热重载）
wails dev

# 仅构建前端
cd frontend && npm run build

# 仅编译 Go 后端
go build ./...

# 完整打包（wails 不在 PATH，需用完整路径）
/c/Users/lu/go/bin/wails build
```

## 打包流程

**必须遵循**：当以下任一情况完成时，触发完整打包：

1. **新功能开发完成** — 功能代码已实现、编译通过、前端构建成功
2. **Bug 修复完成** — 修复已应用、编译通过、逻辑验证无误
3. **前端代码变更** — `frontend/` 下任何文件改动（HTML/CSS/JS）

### 打包步骤

```bash
# 1. 编译检查
go build ./...

# 2. 构建前端
cd frontend && npm run build && cd ..

# 3. Wails 打包（生成可执行文件）
# 注意：wails 不在系统 PATH 中，位于 ~/go/bin/wails.exe
/c/Users/lu/go/bin/wails build
```

产物输出到 `build/bin/kirox.exe`（Windows）或 `build/bin/kirox`（macOS/Linux）。

### 打包前检查清单

- [ ] `go build ./...` 无错误
- [ ] `cd frontend && npm run build` 前端构建成功
- [ ] 前端 `dist/` 目录已更新
- [ ] `/c/Users/lu/go/bin/wails build` 成功生成可执行文件

> **注意**：`wails` 命令不在系统 PATH 中。PowerShell 无法直接运行，需用 Bash 执行完整路径 `/c/Users/lu/go/bin/wails build`。`go` 命令在 PowerShell 中同样不可用，Go 编译检查也需在 Bash 中执行。

## 关键约定

- **Provider 固定为 `"BuilderId"`** — 所有账号注册时 provider 字段硬编码为 BuilderId
- **Region 固定为 `"us-east-1"`** — AWS OIDC 和 Q API 均使用 us-east-1
- **profileArn 来源** — 从 Kiro `refreshToken` 端点响应中提取，不可硬编码
- **指纹缓存** — `browser.IdentityForProxy(proxy)` 按代理地址缓存浏览器指纹，同一代理短时间内复用相同硬件身份
- **错误熔断** — `send-otp 400` 在临时邮箱模式下换邮箱重试，非临时邮箱模式触发 `otpKillOnce` 全局终止
- **账号存储** — 注册成功写入 `accounts.json`，同邮箱覆盖；失败/封号不落盘

## 技术栈

| 层 | 技术 |
|---|---|
| 桌面框架 | Wails v2.12 |
| 后端 | Go 1.25 |
| 前端 | Vanilla JS + HTML + CSS（无框架） |
| HTTP 客户端 | bogdanfinn/fhttp (TLS 指纹伪装) |
| 构建 | Node.js build.js（复制静态文件到 dist/） |
| 打包 | Wails CLI → 单一可执行文件 |
| 平台 | Windows 10+ / macOS / Linux |

## Git 提交规范

```
feat: 中文描述
fix: 中文描述
```

| 前缀 | 用途 |
|------|------|
| `feat:` | 新功能 |
| `fix:` | Bug 修复 |

## 关键注意事项

- **订阅页自动刷新冲突**：订阅页有 3 秒间隔的 `setInterval(reloadSubscriptionAccounts)`，会完全重建 `subState.accounts`。任何修改该数组的异步操作（批量获取、单个获取）都必须先 `stopSubAutoRefresh()` 再 `startSubAutoRefresh()`，否则进度状态会被定时器覆盖。