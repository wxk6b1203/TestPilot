# Contributing Guide

Thank you for your interest in and contributions to **TestPilot**. Please read this guide first — especially the **Contributor License Agreement (CLA)** below: this project is distributed under the Apache License 2.0, and the CLA is a legal prerequisite for accepting contributions.

> For the Chinese version, see [CONTRIBUTING_ZH.md](CONTRIBUTING_ZH.md).

## Project Status

TestPilot is currently in the early planning stage; the repository mainly contains `docs/` and `proto/`. Suggestions and revisions to the design documents, data model and protocol definitions are welcome.

## Commit Conventions

- Commit messages follow the [Conventional Commits](https://www.conventionalcommits.org/) format (see `.github/git-commit-instructions.md`):

  ```
  <type>(<scope>): <subject>
  ```

- **Commit language**: `<subject>` may be written in Chinese or English.

  - **English commits**: use an English summary directly, e.g.:

    ```
    feat(api): add case execution endpoint
    ```

  - **Chinese commits**: MUST include an English summary, appended as a footer with the `English:` prefix, e.g.:

    ```
    feat(api): 新增用例执行接口

    English: Add case execution endpoint
    ```

- **Documentation language**: docs follow the existing convention — Simplified Chinese.

## Contribution Workflow

1. Fork this repository and create a feature branch from `main` (`feat/xxx`, `docs/xxx`, `fix/xxx`);
2. Make your changes and test them;
3. Commit and push to your branch;
4. Open a Pull Request describing the motivation and scope of the changes;
5. Wait for maintainer review and merge.

## Contributor License Agreement (CLA)

> **Important**: This project is distributed under the **Apache License 2.0**, with a **commercial license** available for value-added services (white-label). The Licensor (wxk6b1203) needs to license all code, including your contributions, under both models. The following terms are therefore a **necessary prerequisite** for accepting contributions.

**By submitting a Pull Request, patch, or contributing code, documentation or any other content to this project in any form (a "Contribution"), you represent and warrant that:**

1. **Originality**: the Contribution is your original work, or you are entitled to make the grants below on its behalf; the Contribution does not infringe the intellectual property rights of any third party.
2. **Grant of rights**: you grant the project Licensor a **perpetual, irrevocable, non-exclusive, worldwide, royalty-free** license to:
   - use, copy, modify and distribute your Contribution and its derivative works under the terms of **Apache License 2.0**;
   - use, copy, modify, distribute and sublicense your Contribution and its derivative works under a **commercial license** (including closed-source and proprietary licensing);
   - combine your Contribution with other parts of the project and license the combined work as a whole.
3. **No attribution requirement**: you waive any claim to attribution for the Contribution (your contribution will still be recorded in the project's commit history).
4. **Survival**: this grant survives the merging of your Contribution and cannot be revoked by your subsequent withdrawal from the project.

If you are contributing on behalf of an employer or a third party, please confirm that you have obtained the necessary authorization.

## Need Help?

- Business and technical discussions: see `docs/init.md`, `docs/design.md`
- Commercial licensing inquiries: wxk6b1203@hotmail.com
