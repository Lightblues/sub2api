
## 2026-05-27: integrate inspector into sub2api
除了 sub2api 服务, 之前应该还有个独立的 inspector 服务来检查请求详细数据, 在 :8019 端口.
- 评估一下, 能否将其也合并到 sub2api 服务中? 采用怎么样的方案?
- NOTE: 目前 inspector 用的是 python 简单搭了一个 fastapi 服务, 考虑后续拓展性, 可以使用 ts 重构; 另外最好能和 sub2api 联动, e.g. 点击某条请求 (usage) 跳转到 inspector 对应条目

可视化方案: 我在另一个项目中设计了对于 trajectory 的可视化代码, 在 @sub2api/.tmp/src/ 目录下
- 主要针对的是结构化的 openai 格式的数据, 但优势在于可视化的效果更好, 易于看到整体对话内容;
- 你可以参考来实现 sub2api 场景下请求的可视化 -- e.g. 对于 Anthropic 格式做一些兼容; 支持原始数据展示.
前端路由: 暂时不做权限设计, 所有用户均可见.
INSPECTOR_ARCHIVE_DIR: 这个设计是为了持久化请求数据, 我感觉不应该删掉?

1. vue v.s. react: 理由是什么? 现在 sub2api 是如何实现前端的?
2. chat 数据格式: 现在 sub2api 应该会把原始数据存下来? 那应该两种格式都有.
3. stats 页面: 对于 .tmp/src/ 只参考其对于 messages 数据的可视化, 其他功能都先不管
```sh
- sub2api 本身使用了 vue!
```

// /root/.cursor/plans/integrate_inspector_into_sub2api_93e6468b.plan.md


服务启动了吗? 如何使用?
```sh
功能
  日期选择：顶部下拉框选择日期，点击 "Load" 加载
  统计卡片：显示当日请求数、input/output/cached tokens、session 数
  筛选器：可按 Session ID、API Key、模型、全文搜索过滤
  会话徽章：点击蓝色/粉色 session 标签快速筛选
  请求详情：点击任意行，右侧面板显示 4 个 Tab：
  Chat — 对话可视化（支持 OpenAI 和 Anthropic 格式）
  JSON Tree — 可折叠交互式 JSON 树
  Raw JSON — 原始 JSON 文本
  Headers — 请求头
  导出：点击 "Export JSONL" 下载过滤结果
```

## optim ui
修复:
1. 页面展示: 
  1. 目前下划之后, 上半部分的内容还是被统计数据给占用了, 但我在看数据的时候, 希望对完整的 Chat/json 数据展示 -- 尽量占据整个屏幕
  2. 宽度优化: 目前主体部分左侧是 list 了模型请求, 右边可视化 messages. 右边的内容太窄了 -- 可以跳转到新页面, 或者默认覆盖整个页面?
  3. query list 展示也: 我感觉也可以加到 First Token, Duration 性能指标
2. 优化 chat 展示:
  1. 目前好像没有展示 model response. e.g. 对于 b016e19a-e4e0-4c4b-98fd-64265e8cbaa6 这个 session_id, 其对应唯一一条请求, chat 页面仅显示了输入部分; 但在原始数据的 response_complete.response.output[0].content[0].text 中是有内容的!
3. url 优化: 我在 inspector 页设置筛选规则, or 点选某条请求之后, 当前页面 url 没有变化 -- 我希望将这些放到 url 中, 方便我分享给其他人

另一个优化: 是不是在 https://sn5llmrouter.devcloud.woa.com/admin/usage 页面中, 也可以加上对每个模型请求到 inspector 的跳转链接?

