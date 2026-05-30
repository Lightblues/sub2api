
# Notes
[notes]
- [upstream](https://github.com/Wei-Shaw/sub2api)
- feat: ttft kill @.ea/docs/ttft_timeout.md
  - 问题: 部分 gpt 上游 api 的 first token 超过 1min, 应该是排队/等待导致的 (非异常 <15s)
  - 方案: 在 server 侧增加 ttft timeout 机制 -- 不能上游报错直接返回 error (触发 CC 等下游应用重试)
- feat: llm service proxy
  - 方案: 将 proxy 服务集成到 sub2api 服务 (docker) 中
- inspector:
  - feat: chat 形式显示 request/response 数据
  - feat: openai chat/responses, anthropic messages 格式数据
  - feat: 可折叠交互式 JSON 树; 可复制
  - feat: 从模型 usage 项跳转到某一请求的 inspector 页面
  - feat: 数据导出 -- 筛选之后
- tione
  - 在线服务: [doc](https://cloud.tencent.com/document/product/851/74141)
  - 开发机: [console](https://console.cloud.tencent.com/tione/v2/notebook/list?listTab=instance&regionId=102&workspaceId=0)
  - 镜像仓库: 中卫 [shennong/eason-ubuntu-base](https://console.cloud.tencent.com/tcr/repository/ccr/ccr/shennong/eason-ubuntu-base/102/tagList)
- https
  - `sn5llmrouter.devcloud.woa.com` 只是在办公网/devcloud 网络下有效
  - https 证书由 AIO forward 签发; 但是只是针对了办公网; 而 devcloud 环境下只能通过 http 连接
  - 想要走 http 的话, 只能 1. 在服务侧使用 caddy 作为反向代理; 2. 在使用的机器上安装证书信任.


[todo]
- [x] 明确部署方案: config (docker-compose)
- [ ] 修改目录结构!

