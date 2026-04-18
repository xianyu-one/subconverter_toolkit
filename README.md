# subconverter_configs

用于放置配置订阅转换文件

## 自建服务器

本项目提供了经过自定义的subconverter容器

本项目的subconverter容器特性

- 主要针对Clash进行优化
- 默认配置即可抗dns泄露
- 默认添加大量国内域名，在Fakeip模式下依旧能有良好的局域网访问体验

可以直接使用下面的示例文件进行启动

```
version: '3'
services:
  app:
    image: mrxianyu/subconverter:latest
    container_name: subconverter
    restart: unless-stopped
    ports:
      - '25500:25500'
```