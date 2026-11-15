# Mihomo Manager

一个自托管的 Mihomo Web 管理界面。通过浏览器管理 Sub-Store 订阅、查看和编辑 Mihomo 流量规则，也能从同一入口打开 Zashboard 查看代理策略组、连接和运行状态。

- **订阅管理** · 导入、编辑、删除和导出订阅，查看节点数、流量额度、到期时间和最后更新时间。服务端保存上次成功的结果，支持为每条远程订阅设置自动更新间隔。
- **流量规则** · 查看现网规则，增删、启停和排序，保存浏览器草稿并预览变更。配置可选发布器后，可校验并热加载规则，写入 Mihomo 启动配置，失败时尝试回滚。
- **策略组切换** · 可将 Sub-Store 订阅绑定到已有的 Mihomo 策略组选项，由管理员显式切换。订阅刷新独立运行。

## 自托管

从[部署指南](docs/deployment.md)开始。指南包含普通 Linux 主机上的构建、Mihomo 原生 HTTPS 配置、Sub-Store 和 OIDC 接入、反向代理、首次检查和可选规则发布器配置。

| 页面 | 路径 |
| --- | --- |
| 订阅管理 | `/management/` |
| 流量规则 | `/management/?view=rules` |
| Zashboard | `/ui/` |

独立运行需要自备 Mihomo、Sub-Store、Zashboard 静态文件、OIDC 身份提供方和 HTTPS 反向代理。项目提供管理服务和 Web 界面。
