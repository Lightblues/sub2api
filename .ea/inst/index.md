
# Notes
[notes]
- [upstream](https://github.com/Wei-Shaw/sub2api)
- feat: ttft kill @.ea/docs/ttft_timeout.md
  - 问题: 部分 gpt 上游 api 的 first token 超过 1min, 应该是排队/等待导致的 (非异常 <15s)
  - 方案: 在 server 侧增加 ttft timeout 机制 -- 不能上游报错直接返回 error (触发 CC 等下游应用重试)

[todo]
- [x] 明确部署方案: config (docker-compose)
- [ ] 修改目录结构!

