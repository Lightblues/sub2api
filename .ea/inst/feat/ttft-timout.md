
## bug/feat: ttft timtout kill
发现一个奇怪的 bug: 对于上游的 gpt 的请求, 一般的 first token 都是在 5s 以内的, 但是有些异常请求的首 token 要 100s+ 才返回; 后续的输出速度时候正常的.
- 假设: 我猜可能是上游服务的排队可能造成了异常;
- 策略: 一个简单的策略是, 若上游 first token 超过 15s 都没有响应, 则直接标记该条请求为 failed -- 这样, 触发下游的应用层 (e.g. Claude Code) 的重试机制.
帮我调研一下这套方案的可行性?
- 方案构想: 是不是架构上不需要太多改动? e.g. 在模型请求的时候带上 15s 的timeout 参数即可?
- 控制影响范围: 未避免其他影响其他的 api 服务, 该项策略我觉得仅针对 `ian_private` 这个 group 生效即可.

合理, 开始实现!

针对你的实现, 在 @sub2api/.ea/docs/README.md 目录下写一个 report

我们之前针对一个 bug 实现了基于 ttft 的一个 feat, (git added), 参见 @.ea/docs/ttft_timeout.md , 帮 review/text 一下是否合理?
```sh
1. 设计正确性 ✅
  「首 token」口径与 first_token_ms 一致：都走 openAIStreamDataStartsClientOutput，preamble（response.created / response.in_progress）不重置计时器、也不算 TTFT，与文档第 44–46 行约定一致。
  从请求开始计时 vs 从「上游 200 OK」计时：当前 startTime 是 Forward 入口传入的 startTime（forwardOpenAIDirect 调用前已记录），所以 TTFT 实际包含了 DNS/连接/TLS/排队/header。这正是要解的问题（上游排队），口径正确，文档也讲清楚了。
  与 stream_data_interval_timeout 正交：interval 看的是「读字节间隔」，TTFT 看的是「首个非 preamble 事件」，二者都在 select 中独立分支，互不干扰。✅
  failover 行为：超时后返回的是普通 error，不是 UpstreamFailoverError；上游一旦写过 200 SSE header，再切账号容易出现协议混乱，不做 failover 是正确选择，依赖客户端重试（文档 §限制 1 已说明）。
```

OK, 帮我实现一下