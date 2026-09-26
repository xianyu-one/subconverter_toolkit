# Windows FlClash 首版采用原生 Observer

FlClash 当前生成的 External Controller 配置只允许关闭或绑定 `127.0.0.1:9090`，容器不能假定可直接连接 Windows 主机的回环监听。首版 Windows 部署使用原生 Observer；Docker 仍支持容器可访问 Controller 的环境。这样避免引入端口转发代理或要求用户暴露 Controller 到局域网，同时保留统一的程序配置与数据库格式。
