# Go Electricity CLI

获取青海大学校园卡电费页中的剩余电量。

默认读取系统里上一次绑定的宿舍房间，并查询该房间当前剩余电量。

## 运行

```bash
cd /home/server/Auto-Qhu/go-electricity-cli
go run . -openid '你的 openid'
```

也可以使用环境变量：

```bash
cd /home/server/Auto-Qhu/go-electricity-cli
OPENID='你的 openid' go run .
```

输出 JSON：

```bash
OPENID='你的 openid' go run . -json
```

如果系统里没有保存上次绑定的宿舍，可以手动覆盖：

```bash
OPENID='你的 openid' go run . -roomid '房间ID' -factorycode 'N002'
```

## 实现方式

请求链路：

1. 访问首页拿会话 cookie
2. 访问电费页并解析 `idserial`、`username`、`tel`
3. 调 `querywechatUserLastInfo` 读取上次绑定的宿舍
4. 调 `queryFloorList` 完成页面同样的注册初始化
5. 调 `queryRoomList` 获取 `quantity`
