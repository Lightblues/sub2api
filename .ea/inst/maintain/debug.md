## 2026-05-27: debug CC too slow
文档参考: @sub2api/.ea/docs/README.md
对于本机启动的 sub2api 服务, 帮我检查一下 id=6 的 key (应该叫 ianxxu) 目前所使用的 group 是哪一个?
根本问题: 为什么我用了 eason 这个 key (ian_private group), 但是请求记录似乎没有保存下来? (无法在 inspector 中看到)
// 只是延迟

可能是数据没及时刷新? 现在有了!
现在是不是只能显示成功的请求? 
- 我的问题是: CC 接入这个 key 之后, 现在运行卡住了;
- 你可以分析这个 key 最后几个请求, 定位问题为什么卡住吗?
// CC 执行 bash 太慢!
