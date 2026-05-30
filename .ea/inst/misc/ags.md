
## 2026-05-30: test `ags` skill
帮我测试跑一个完整的 agent rollout 看看, 脚本写到 @scripts/ 目录下

我在 .env 中设置了 ANTHROPIC_BASE_URL & ANTHROPIC_AUTH_TOKEN, 你可以使用. 

所用的 ags tool id 是什么? 我是不是可以在网页上看到/操纵沙盒工具?
我在 [web](https://console.cloud.tencent.com/ags/sandbox/detail?rid=22&sandboxId=sdt-535ctmbe) 上看到这个沙盒了, 但所有实例都停了, 帮我新建一个, 不要停止, 我登录测试

你上面为什么要 monkey patch _common? 因为 e2b 的版本问题吗?
"编码了 API key 校验" 设置 e2b 包的哪个文件? 什么版本的 e2b
```sh
e2b 上游有意做的本地 key 格式校验（写死 ^e2b_[0-9a-f]+$），跟 AGS 的 ark_ 前缀冲突
```
