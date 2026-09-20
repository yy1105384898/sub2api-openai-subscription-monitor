# OpenAI 订阅监控（Sub2API 插件）

用于 [ranxi2001/sub2api](https://github.com/ranxi2001/sub2api) `v2.7.4` 的可安装插件。插件 ID 为 `yangyang.openai.subscription-monitor`，兼容范围锁定为 `>=2.7.4 <2.8.0`。其他版本未验证，不要强行安装。

插件从宿主账号目录枚举已登录的 OpenAI OAuth 账号，复用宿主提供的临时身份和账号代理，不发起新登录，也不读取数据库。账号列表显示套餐、订阅到期/续费状态及 5 小时、7 天窗口摘要；账号旁的统计图按钮打开二级详情：

- 官方 `/backend-api/wham/usage`：窗口用量及重置时间、Credits 积分余额/状态、近似本地与云端消息额度、重置卡数量。
- 官方 `/backend-api/wham/rate-limit-reset-credits`：可用重置卡及逐张到期时间。只保留到期时间，不保存卡 ID；本插件不会消费重置卡。
- 官方 `/backend-api/wham/analytics/daily-workspace-usage-counts`：最近 7 天 Credits、Token、轮次、每日趋势、客户端拆分及模型轮次。模型接口没有可靠的逐模型 Credits 成本，界面不伪造这项数值。当天数据可能尚未结算。
- 官方 `/backend-api/accounts/check/v4-2023-04-27` 和 `/backend-api/subscriptions`：套餐、订阅到期及续费状态；接口未提供字段时显示未知。

**口径说明：**“积分余额”是官方 `credits.balance`，不是 API Key 余额；“已用 Credits”是最近 7 天官方日统计的消耗，两者不能直接相减。美元折算使用当前实现中的 `25 credits = 1 USD`，仅用于参考；上游口径变动时应更新实现。站内 API 用量日志与官方统计不是同一口径。

## 安装与升级

1. 从 [Releases](https://github.com/yy1105384898/sub2api-openai-subscription-monitor/releases) 下载 `openai-subscription-monitor-0.2.0.s2plugin`；公钥见仓库的 [`dist/publisher-public-key.txt`](dist/publisher-public-key.txt)。
2. 在 Sub2API 服务端配置可信发布者公钥。将公钥文件中的 `key_id=base64_public_key` 拆为配置映射（只配置公钥，绝不使用 `.keys/publisher-private.key`）：

   ```yaml
   plugins:
     trusted_publishers:
       yangyang-openai-monitor-v1: <publisher-public-key.txt 中等号右侧的值>
   ```

3. 备份 Sub2API 配置并重启服务，使可信发布者配置生效。在管理后台的“插件”页面上传 `.s2plugin`，按后台要求完成管理员二次验证。首次建议以 `1%` 灰度启用，确认正常 API 转发后再扩大灰度。监控会读取宿主允许的全部 OpenAI OAuth 账号，不依赖灰度比例。
4. 打开插件配置页点击“立即刷新”，确认账号、官方窗口及统计数据；再用原有 API Key 做一次真实转发检查。升级时上传新版包，保留现有账号和宿主数据，按后台流程切换版本即可。

安装包不能通过复制到服务器目录自动启用；必须经 Sub2API 的插件管理流程验签、安装和启用。插件无法替代管理员二次验证。

## 设置与隐私

刷新间隔默认 30 分钟（范围 5～1440），请求超时默认每次 20 秒，并发检查数默认 3。可关闭官方用量同步，或使用“账号邮箱和 ID 脱敏显示”开关；脱敏默认开启，关闭后保存并刷新会展示完整邮箱和账号 ID。

插件**不持久化 OAuth Token**；但会把账号状态快照写入宿主插件 KV。关闭脱敏后，KV 快照及管理端状态响应中也会包含完整邮箱和账号 ID，请只对可信管理员开放插件配置页。旧快照要等下一次成功刷新后才会被新脱敏设置覆盖。上游查询失败时保留错误/未知状态，不把失败当作零额度。

插件声明 `openai.oauth.outbound_transport.v1` 能力，因此还实现透明 HTTP 转发。启用前先灰度和验证原有请求链路；不可把它当成纯离线报表插件。

## 从源码构建与验证

需 Go 1.25+ 和 PowerShell。克隆仓库后执行：

```powershell
go test ./internal/... ./cmd/... ./pluginapi/...
./build.ps1 -Version 0.2.0
go run ./cmd/runtimecheck -package dist/openai-subscription-monitor-0.2.0.s2plugin -public-key dist/publisher-public-key.txt
```

`build.ps1` 会生成 Linux amd64 与 Windows amd64 运行时并用**本机** `.keys/publisher-private.key` 签名。仓库不包含私钥；自行构建若生成新私钥，必须把**新公钥**配置到目标 Sub2API，仓库提供的公钥无法验证你的本地新包。官方发布包使用仓库所示公钥。`runtimecheck` 验证文件哈希、签名、运行时握手、配置与透明转发，不会连接真实 OpenAI 账号。
