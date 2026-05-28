## 2026-05-22: integrate `youtu_llm_proxy.py`
本机的 sub2api 服务应该还依赖另一个中转服务 @youtu_llm_proxy.py , 帮我检查一下这个服务还在吗? 他是如何配置到 sub2api 服务中的?
ref: @sub2api/src/.ea/docs/README.md

如何调用该服务?
```sh
curl -X POST http://127.0.0.1:8088/v1/messages \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-opus-4-6","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}'

  # Claude Code
export ANTHROPIC_BASE_URL="http://localhost:80"
export ANTHROPIC_AUTH_TOKEN="sk-xxx"  # youtu-claude 分组下的 key
claude

# 或直接 curl
curl -X POST http://localhost:80/v1/messages \
  -H 'Content-Type: application/json' \
  -H 'x-api-key: sk-xxx' \
  -d '{"model":"claude-sonnet-4-6","max_tokens":50,"messages":[{"role":"user","content":"ping!"}]}'
```

我希望将 proxy 服务集成到 sub2api 镜像中, 你觉得怎样比较好?
// /root/.cursor/plans/embed_proxy_in_image_447ddc94.plan.md

你已经重启了镜像了是吧? 原本的停了?

下面, 我想调整项目结构:
- 目前在 /root/sub2api 目录放了一些配置文件 & 项目源码, 这样开发很混乱.
- 应该把项目代码直接放到 /root/sub2api/ 顶层目录; 相关配置文件放到项目内 (有隐私的话 .gitignore) -- 这样是不是更规范?

检查一下 @sub2api/.ea/docs/README.md 目录的相关文件, 是不是需要更新一下? (有旧内容的话也update一下)

