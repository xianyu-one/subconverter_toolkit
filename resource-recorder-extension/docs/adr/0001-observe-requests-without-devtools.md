# 在普通浏览会话中观察请求

扩展在用户主动开启的记录期间观察各标签页的页面请求，首版不要求打开开发者工具。这让用户在日常浏览时就能查看请求并复制候选规则；代价是只能展示浏览器扩展接口实际提供的请求，且响应的声明大小不能当作实际传输字节数。界面必须明确标出未知值和记录缺口，不得把观察结果描述为完整网络抓包。

参考：[Chrome webRequest](https://developer.chrome.com/docs/extensions/reference/api/webRequest)、[Chrome DevTools Network](https://developer.chrome.com/docs/extensions/reference/api/devtools/network)。
