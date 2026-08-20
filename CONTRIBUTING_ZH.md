# 贡献指南（Contributing Guide）

感谢您对 **TestPilot** 的关注与贡献。请先阅读本指南，尤其是下方的**贡献者许可协议（CLA）**声明——本项目采用 Apache License 2.0 授权，该声明是法律前提。

> 英文版见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 项目状态

TestPilot 当前处于早期规划阶段，仓库内以 `docs/` 与 `proto/` 为主。欢迎对设计文档、数据模型、协议定义提出建议与修订。

## 提交规范

- 提交信息遵循 [Conventional Commits](https://www.conventionalcommits.org/) 格式（详见 `.github/git-commit-instructions.md`）：

  ```
  <type>(<scope>): <subject>
  ```

- **提交语言**：`<subject>` 可使用中文或英文。

  - **英文提交**：直接使用英文简述，例如：

    ```
    feat(api): add case execution endpoint
    ```

  - **中文提交**：**必须附英文简述**，以 `English:` 前缀作为 footer 追加在提交信息末尾，例如：

    ```
    feat(api): 新增用例执行接口

    English: Add case execution endpoint
    ```

- 文档语言：与现有文档一致，使用简体中文。

## 提交流程

1. Fork 本仓库，基于 `main` 创建特性分支（`feat/xxx`、`docs/xxx`、`fix/xxx`）；
2. 完成修改并自测；
3. 提交并推送到您的分支；
4. 发起 Pull Request，在描述中说明改动动机与影响范围；
5. 等待维护者评审与合并。

## 贡献者许可协议（CLA / Contributor License Agreement）

> **重要**：本项目采用 **Apache License 2.0** 授权，并提供**商业授权**用于增值服务（白标）。授权方（wxk6b1203）需要将包括您贡献在内的全部代码按两种模式对外授权，因此以下条款是接受贡献的**必要前提**。

**通过提交 Pull Request、Patch 或以其他形式向本项目贡献代码、文档或任何内容（"贡献"），您即表示并保证：**

1. **原创性**：该贡献是您的原创作品，或您有权就该贡献作出下述授权；您的贡献不侵犯任何第三方的知识产权。
2. **授权范围**：您授予项目授权方一项**永久、不可撤销、非独占、全球范围、免版税**的许可，允许授权方：
   - 以 **Apache License 2.0** 条款使用、复制、修改、分发您的贡献及其衍生作品；
   - 以**商业授权**（含闭源、专有授权）条款使用、复制、修改、分发、再许可您的贡献及其衍生作品；
   - 将您的贡献与项目其他部分结合，并作为整体对外授权。
3. **无需署名**：您放弃对贡献的署名权主张（项目仍会在提交历史中记录您的贡献）。
4. **持续有效**：本授权在您的贡献被合并后持续有效，不因您退出项目而撤销。

如您代表雇主或第三方贡献，请确认您已取得相应授权。

## 需要帮助？

- 业务与技术讨论：参见 `docs/init.md`、`docs/design.md`
- 商业授权咨询：请留ISSUE/讨论或者其他方式联系我。
