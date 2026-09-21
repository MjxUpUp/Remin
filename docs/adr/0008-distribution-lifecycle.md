# ADR-0008 分发与生命周期：npm 获取 + 落位 + 台账 + 自更新 + 干净卸载

- 状态：accepted（2026-09-21）

## 背景

v0.2.0 的分发现实：版本号是硬编码 const、CI 只有测试门禁、无发版产物、安装方式只有「clone 源码 + make build」；agent 接线钉死安装时刻二进制的绝对路径（clone 目录删除即全线失联）；没有卸载命令，`doctor --install` 写 5 处配置 + 无限累积的备份文件，手工无法摘净。产品化承诺四件事：**自动升级、发版走 CI、一次安装后不再干扰、一次卸载完全干净**。

## 决策

### 1. 分发主渠道：npm（`@reminmem/remin`），获取与运行分离

- npm 主包是薄壳 launcher（`bin/remin.js`），平台二进制走 esbuild 式 optionalDependencies 平台包（`@reminmem/remin-<os>-<arch>`，os/cpu 字段让 npm 自动跳过不适配平台）。
- 选型理由：目标用户（Claude Code / Codex / Gemini CLI 用户）全部是 npm 生态一等公民；跨平台一套机制；中国大陆网络可及（npmmirror 镜像，对比 GitHub Releases 直下不稳）；升级/卸载语义现成。
- 已知短板（npm 的 nvm 路径漂移问题，Claude Code 官方为此转向原生安装器）由下一条「落位」根治——npm 只当获取管道。

### 2. 落位（staging）：接线一律钉 `~/.remin/bin/remin`

- `doctor --install` 首次运行把二进制复制到 `<root>/bin/remin`（原子替换），所有 agent 配置只指向该稳定路径；运行时真身永远是落位副本，渠道目录（npm 全局/brew/clone）永不参与。
- nvm 切版本、npm 目录被清、换渠道重装——已接线配置零影响；升级 = 落位原位替换，配置零改动。
- 落位产物（`bin/`、`wiring.json`）进真源 `.gitignore`（`git add -A` 提交路径防扫入）；未初始化的 root 不制造 `.gitignore`（等 `remin init` 写新默认值）。
- 与真源同根：记忆归用户所有，可执行文件也住在用户领地——卸载一个根目录收净。

### 3. 卸载：cordis 可逆性——接线台账即 undo log

- `doctor --install` 的每个 effect（JSON 键 / hook 条目 / TOML 段 / gitignore 行）记入 `<root>/wiring.json`：文件、键、精确写入内容、备份路径、是否我们创建的文件。
- `remin uninstall` 逆序回放 inverse：摘键（按点分路径下钻）、按 command 精确匹配摘 hook 条目（不动用户其它 hooks）、摘 TOML 段、摘 gitignore 行、删全部 `.remin-backup-*`、删落位、删台账、删我们创建的空壳文件。
- 回放前校验现值仍是我们写入的值：被用户手改 → 跳过并如实报告，绝不盲删（「宁可不知道，不能自信地错」的安装面版本）。
- 台账丢失（v0.2.0 时代接线 / 用户删过 root）→ 退化为启发式扫描：命令 basename 为 remin 且路径含 `.remin` 或以 `/bin/remin` 结尾才摘——保守匹配，不误伤他人 memory server。
- 真源默认保留（记忆是用户资产，P4）；`--purge` 显式 opt-in 才删，删除前如实报告记忆条数。

### 4. 升级：`remin upgrade` 直连 npm registry

- 流程：查 `@reminmem/remin` latest → 平台包元数据（`dist.tarball` + `dist.integrity` sha512）→ 下载 → 完整性校验（不符拒绝安装）→ 解出 `package/bin/remin` → 原子替换落位（tmp + rename；运行中进程持旧 inode 不受影响，Windows 撞占用时 rename-aside 兜底）。
- 落位不存在 → 指引 `doctor --install`；渠道内更新走渠道（`npm update -g`）。
- registry 可经 `REMIN_NPM_REGISTRY` 覆盖（镜像）。纯标准库实现（宪法核心依赖白名单零新增）。
- 干扰控制：版本新提示只在 `remin version` 主动查询时出现（24h 缓存，失败静默，`REMIN_NO_UPGRADE_CHECK=1` 可关）；无静默自动升级——可信记忆层静默自改二进制与用户主权气质不合。

### 5. 发版：tag 驱动，GitHub Actions → npm（绝不在本机 publish）

- `git tag vX.Y.Z && git push --tags` → `release.yml`：五平台交叉构建（CGO_ENABLED=0，ldflags 注入版本）→ `scripts/npm-release-prep.sh` 组装 npm 包（本地验证与 CI 共用）→ 先发 5 个平台包再发主包（scoped 公开包 `--access public`）→ GitHub Release 附二进制 + SHA256SUMS。
- workflow_dispatch 支持演练模式（构建 + npm pack 内容验证，不 publish）。
- 认证：仓库 secret `NPM_TOKEN`（@reminmem 组织 automation 角色）。仓库 private 期间 npm provenance 不适用；完整性由 registry integrity（sha512）与 SHA256SUMS 双重保障，转 public 后可加 `--provenance`。
- 版本号：`cli.Version` 为 var（ldflags 注入点），源码构建回落内置默认值；Makefile 注入 `git describe`。

### 6. License：MIT

最大通行度压倒一切（被 agent 厂商捆绑/预装零阻力，npm 公开发布与 Homebrew tap 的通行证）；单二进制 CLI 无专利池诉求；AGPL/GPL 会吓退企业生态集成，与「中立资产层」定位（P1/N4）相冲。

## 备选与否决

- **纯 npm 路径接线（不落位）**：nvm 切版本即全线失联（Claude Code 前车之鉴）——否决。
- **原生 install.sh 为主渠道**：路径最稳但需自建分发基础设施 + 下载源国内不可靠 + `curl | sh` 信任面——留作二期辅渠道。
- **Homebrew 为主渠道**：缺 Windows、国内体验一般、tap 要求 public——留作二期辅渠道。
- **go install**：要求 Go 工具链，目标用户大多没有——否决。
- **卸载时重新扫描猜测**：误伤面不可控，违反 cordis 可逆性——否决（仅作台账丢失时的保守兜底）。
- **静默自动升级**：与「可信记忆层」的用户主权气质不合——否决（提示 + 手动升级）。

## 后果

- 正：四体验闭环（npm 一装 / 落位后零干扰 / upgrade 原位换身 / uninstall 台账摘净）；宪法零违反（纯 stdlib，零新依赖）；CI 发布单一通道。
- 负：落位副本与渠道副本存在版本漂移可能（`remin version`/`doctor` 提示 `remin upgrade` 缓解）；发布依赖 NPM_TOKEN secret 一次性配置；Windows 上运行中 exe 替换依赖 rename-aside（极端情况留 `.old` 待下次清理）。
- 后续（触发条件明确）：仓库转 public 后 release.yml 加 `--provenance`；二期上 install.sh / Homebrew tap 辅渠道；`doctor` 增加落位健康自检（钉死路径失联时主动报警并自愈重接线）。
