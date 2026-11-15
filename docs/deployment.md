# 自托管部署

下面以 Linux 单机为例。Mihomo、Sub-Store 和管理服务只监听回环地址，由 Caddy 提供浏览器访问的 HTTPS 入口。将 `manager.example.com` 替换成你自己的域名。为该域名配置 DNS，并准备浏览器信任的 HTTPS 证书。

命令在 Bash 中以普通服务账户运行，浏览器入口使用标准 HTTPS 端口 443。构建使用 Go 1.27.3、Node.js 24.12.0 和 npm。示例还用到 OpenSSL、curl 和 unzip。准备可执行的 [Mihomo](https://github.com/MetaCubeX/mihomo/releases) 与 [Caddy](https://caddyserver.com/docs/install)。运行规则发布器时还需要 Linux 的 `flock`。

## 1. 构建管理服务

从仓库根目录运行。后续命令使用相同的 `APP` 路径。

```sh
umask 077
export APP="$HOME/.local/share/mihomo-manager"
mkdir -p "$APP"/{bin,credentials,management-ui,zashboard,sub-store/data,mihomo,subscription-state}

npm --prefix subscriptions ci
npm --prefix subscriptions run build
go -C gateway build -o "$APP/bin/mihomo-manager" .
cp -R subscriptions/dist/. "$APP/management-ui/"

curl -fL -o "$APP/zashboard/zashboard.zip" \
  https://github.com/Zephyruso/zashboard/releases/download/v3.30.0/dist-no-fonts.zip
unzip -o "$APP/zashboard/zashboard.zip" -d "$APP/zashboard"
test -f "$APP/zashboard/dist/index.html"
```

Zashboard ZIP 可以从[上游发行页](https://github.com/Zephyruso/zashboard/releases)换成其他兼容版本，解压后应保留 `zashboard/dist/index.html` 这个目录结构。

## 2. 启用 Mihomo 原生 HTTPS

已有实例可将下面的控制项合并到现有配置，并保留已有代理、策略组和规则。下面的命令生成供单机示例使用的证书与 API 密钥。Mihomo 与管理器分机时，证书 SAN 应覆盖管理器连接使用的主机名或 IP，并在控制端口的防火墙只允许管理主机访问。

```sh
umask 077
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
  -nodes -days 365 -subj '/CN=127.0.0.1' \
  -addext 'subjectAltName=IP:127.0.0.1' \
  -keyout "$APP/mihomo/controller.key" -out "$APP/mihomo/controller.crt"
cp "$APP/mihomo/controller.crt" "$APP/credentials/mihomo-controller-ca.crt"
openssl rand -hex 32 > "$APP/credentials/mihomo-controller-token"
```

以下命令创建供新实例使用的最小配置。已有实例只合并控制器相关字段，并将 `secret` 设为管理服务凭证文件里的值。

```sh
token=$(cat "$APP/credentials/mihomo-controller-token")
cat > "$APP/mihomo/config.yaml" <<EOF
external-controller: 127.0.0.1:9090
external-controller-tls: 127.0.0.1:9443
secret: "$token"
tls:
  certificate: ./controller.crt
  private-key: ./controller.key
mode: rule
rules:
  - MATCH,DIRECT
EOF
unset token

mihomo -t -d "$APP/mihomo" -f "$APP/mihomo/config.yaml"
mihomo -d "$APP/mihomo" -f "$APP/mihomo/config.yaml"
```

在另一个终端设置同样的 `APP`，确认 HTTPS API 可访问。这个请求使用证书校验和 API 密钥。

```sh
printf 'header = "Authorization: Bearer %s"\n' "$(cat "$APP/credentials/mihomo-controller-token")" |
  curl --config - --fail --silent --show-error \
    --cacert "$APP/credentials/mihomo-controller-ca.crt" https://127.0.0.1:9443/version
```

[上游 Mihomo 控制器和 TLS 配置](https://wiki.metacubex.one/en/config/general/)

## 3. 启动 Sub-Store

可以接入已有的 Sub-Store Node 后端，管理服务需要访问它的 `/api/subs` 和 `/download/`。若新建后端，在独立终端运行下面的命令。

```sh
curl -fL -o "$APP/sub-store/sub-store.bundle.js" \
  https://github.com/sub-store-org/Sub-Store/releases/download/2.44.14/sub-store.bundle.js

export SUB_STORE_BACKEND_API_HOST=127.0.0.1
export SUB_STORE_BACKEND_API_PORT=3000
export SUB_STORE_DATA_BASE_PATH="$APP/sub-store/data"
export SUB_STORE_CORS_ALLOWED_ORIGINS=https://manager.example.com
node "$APP/sub-store/sub-store.bundle.js"
```

在另一个终端运行 `curl -fsS http://127.0.0.1:3000/api/subs`。新建实例应返回成功状态和空订阅列表。[Sub-Store 服务端配置](https://github.com/sub-store-org/Sub-Store/blob/master/config/README.md)

## 4. 配置 OIDC 和管理服务

在身份提供方创建一个带客户端密钥的 Web 应用，使用 Authorization Code + PKCE（S256），回调设为 `https://manager.example.com/sso/callback`。获取 HTTPS Issuer、客户端 ID 和密钥。访问名单使用该应用收到的 ID Token 中的 `sub`，多个值用空格分隔。

```sh
umask 077
printf '%s\n' 'YOUR_CLIENT_ID' > "$APP/credentials/oidc-client-id"
read -rsp 'OIDC 客户端密钥: ' oidc_secret
printf '\n'
printf '%s\n' "$oidc_secret" > "$APP/credentials/oidc-client-secret"
unset oidc_secret
openssl rand -hex 32 > "$APP/credentials/session-signing-key"
```

把下列内容保存为 `$APP/manager.env` 并替换示例值。如使用已有的 Sub-Store，也在这里改成对应的后端地址。初次生成的会话签名密钥保留在凭证目录，换密钥会使现有登录失效。

```sh
MANAGER_ORIGIN=https://manager.example.com
MIHOMO_CONTROLLER_URL=https://127.0.0.1:9443
SUBSCRIPTION_BACKEND_URL=http://127.0.0.1:3000
OIDC_ISSUER=https://identity.example.com/realms/example
OIDC_ALLOWED_SUBJECTS="YOUR_OIDC_SUBJECT"
GATEWAY_LISTEN=127.0.0.1:8080
```

启动管理服务。

```sh
set -a
. "$APP/manager.env"
set +a
export MANAGER_DATA_DIR="$APP"
"$APP/bin/mihomo-manager" "$APP/credentials"
```

## 5. 开启浏览器入口并检查

将下列站点块加入 Caddy 配置。已有其他反向代理时，要把请求的 `Host` 和可信的 `X-Forwarded-Proto: https` 传给管理服务，并代理 WebSocket。

```caddyfile
manager.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

用 `caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile` 校验，再使用系统服务管理器加载配置，例如 `sudo systemctl reload caddy`。仅在局域网使用时，可以改用内部 CA，例如 Caddy 的 `tls internal`，并在客户端安装对应的根信任证书。[Caddy 反向代理配置](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)

1. 打开 `https://manager.example.com/management/`，用访问名单里的账号登录。
2. 在「订阅管理」导入自己的订阅，点「更新」检查节点数和服务商提供的用量。在「自动更新设置」里填分钟数，`0` 为关闭。
3. 打开「流量规则」，确认能读到 Mihomo 的规则。打开 `/ui/` 检查 Zashboard，后端地址使用同一个 HTTPS 站点，Mihomo API 密钥由管理服务在服务器端提供。

常驻运行时，用 systemd 或其他服务管理器托管三个启动命令。管理服务的 systemd 服务可用如下配置，替换 `/home/deploy` 和服务用户。Sub-Store 对应的启动命令和环境变量见第 3 节，已由系统管理的 Mihomo 服务继续使用自己的启动方式。

```ini
[Unit]
Description=Mihomo Manager
After=network-online.target

[Service]
User=deploy
Environment=MANAGER_DATA_DIR=/home/deploy/.local/share/mihomo-manager
EnvironmentFile=/home/deploy/.local/share/mihomo-manager/manager.env
ExecStart=/home/deploy/.local/share/mihomo-manager/bin/mihomo-manager /home/deploy/.local/share/mihomo-manager/credentials
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

保存为 `/etc/systemd/system/mihomo-manager.service` 后运行 `sudo systemctl daemon-reload` 和 `sudo systemctl enable --now mihomo-manager`。

## 可选功能

### 绑定可切换的订阅

在 Mihomo 中准备一个名为 `Sub-Store 订阅` 的 `select` 策略组，添加希望切换的选项。将以下示例保存到 `$APP/credentials/subscription-selectors.json`。左边是 Sub-Store 的订阅 ID（API 的 `name`），右边是该策略组中的现有选项名。每个选项只绑定一次。保存后重新打开管理页即可读到绑定。

```json
{
  "provider-a": "线路 A",
  "provider-b": "线路 B"
}
```

### 校验并应用流量规则

基础部署支持查看规则和在浏览器编辑、保存草稿。要从页面应用变更，将发布器部署在 Mihomo 所在的 Linux 主机，保证它能调用实际运行版本的 Mihomo、读写其启动配置和备份目录，并访问仅本机可用的控制套接字。首次启用套接字时按你自己的 Mihomo 维护流程加载该配置。

同机示例在 Mihomo 的启动配置里加上如下字段。使用 `700` 权限限制父目录，套接字由运行 Mihomo 的服务账户访问。

```yaml
external-controller-unix: ./controller.sock
```

构建发布器。这里假定管理服务和 Mihomo 使用同一个服务账户，`mihomo` 在 `PATH` 中。如果路径不同，将包装脚本里的参数换成实际绝对路径。

```sh
mkdir -p "$APP/rule-backups"
go -C gateway build -o "$APP/bin/rule-publisher" ./cmd/rule-publisher
MIHOMO_BIN=$(command -v mihomo)
test -x "$MIHOMO_BIN"
cat > "$APP/bin/publish-rules" <<EOF
#!/bin/sh
set -eu
umask 077
exec flock -x "$APP/rule-backups/publish.lock" "$APP/bin/rule-publisher" \\
  "$APP/mihomo/config.yaml" "$APP/mihomo/controller.sock" "$MIHOMO_BIN" "$APP/rule-backups"
EOF
chmod 700 "$APP/bin/publish-rules"
```

在 `manager.env` 中加入 `RULE_PUBLISHER_COMMAND=/home/deploy/.local/share/mihomo-manager/bin/publish-rules`，替换成实际绝对路径，再重启管理服务。打开「流量规则」，编辑草稿后点「校验并预览」，确认后才会热加载并写入启动配置。发布器保留启动配置备份并在应用失败时尝试回滚。分机部署可用仓库中的 `deploy/rule-publisher-ssh.sh`，将 SSH 目标限制为只执行发布命令。

## 备份和升级

- 备份 `credentials/`、`manager.env`、`sub-store/data/`、`subscription-state/` 和自己的 Mihomo 配置。配置发布器时一并备份 `rule-backups/`。订阅链接和凭证应留在你的部署环境里。
- 更新代码后重新执行第 1 节的前端构建和 Go 构建，替换静态文件和管理服务二进制，再重启管理服务。如有发布器也重新构建。建议先备份旧二进制和静态文件，便于回退。
- 管理服务默认将订阅数据和调度持久化在 `$MANAGER_DATA_DIR/subscription-state/details.json`。可设置 `SUBSCRIPTION_STATE_DIR` 指向另一个可写目录。容器部署时把该目录挂载到持久卷。
