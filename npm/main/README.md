# @reminmem/remin

> 换脑不换忆，用 Remin。

个人跨 agent 可信记忆层：记忆以人类可读的 markdown 归你所有（`~/.remin/`），跟随你在任何 AI 工具之间无缝流转。

## 安装

要求：Node ≥ 18、git。中国大陆建议镜像：`npm config set registry https://registry.npmmirror.com`；升级检查走镜像：`REMIN_NPM_REGISTRY=https://registry.npmmirror.com`。

```bash
npm install -g @reminmem/remin
remin init               # 创建记忆真源（~/.remin，个人 git 仓库）
remin doctor --install   # 落位二进制到 ~/.remin/bin 并接线全部已装 agent
```

npm 只是获取渠道：`doctor --install` 会把二进制落位到 `~/.remin/bin/remin` 并让
所有 agent 配置指向该稳定路径——之后 nvm 切版本、npm 目录变化都不影响已接线配置。

## 升级 / 卸载

```bash
remin upgrade            # npm registry → 完整性校验 → 原位替换（配置零改动）
remin uninstall          # 按接线台账精确摘除全部接线（默认保留记忆）
remin uninstall --purge  # 连同 ~/.remin 记忆真源一并删除
npm uninstall -g @reminmem/remin   # 渠道命令本体收尾（remin uninstall 之后）
```

详细文档见 [主仓库](https://github.com/MjxUpUp/Remin)。
