# .octospec — octo-cli architectural constraints

`.octospec/` 存放 **octo-cli 仓库专属的架构约束（rules）**，供 AI 执行器（Claude Code 等）在改动
对应代码时自动注入，使生成的改动符合本仓的真实模式与安全边界。结构与 octo-server 同构。

## 什么是 rule

每条 rule 是一段可注入的约束，落在 `rules/<id>.md`，frontmatter 携带可被工具解析的扩展字段：

| 字段 | 含义 |
|------|------|
| `id` | 全局唯一标识 |
| `tier` | `repo`（仓库级）/ 继承的全局级 |
| `priority` | 预算受限时的排序权重（越大越先注入） |
| `load_bearing` | 是否为「红线」：架构根基 / 安全边界 / wire 契约。预算受限时优先保留 |
| `inject_when.paths` | 命中这些路径 glob 时注入 |
| `inject_when.touches` | 命中这些主题标签时注入（与 paths 取并集） |
| `source` | `self` 或继承来源（如 `octo-spec@1.1.0`） |

## 消费方式

- 工具读 `rules/_index.yaml`（镜像各 rule 的 frontmatter）来决定注入哪些 rule，无需逐文件解析。
- 注入触发：`inject_when.paths` glob **或** `inject_when.touches` 标签命中（并集），每条注入一次。
- 预算受限时：先保 `load_bearing: true`，再按 `priority` 降序；被截断的 rule 仅注入 summary 行。

## 当前 rules（11 条 = 7 load-bearing + 4 约定级）

详见 `rules/_index.yaml`。load-bearing 7 条：envelope-io-contract、credential-at-rest、
metadata-driven-registry、credential-resolution、error-taxonomy、factory-di-no-globals、transport-client。

## 维护约定

- **改了真实代码模式，同步改 rule**——rule 描述的是代码里实际存在的模式，不是愿望。
- **rule 正文引用 `CLAUDE.md` 对应段落（`> See CLAUDE.md § "…"`），不复制大段**，避免两份漂移。
- rule 基于真实代码核实产出，不凭印象；未在代码中出现的模式（如服务端才有的 rate-limit /
  space-isolation）不产出 rule。
