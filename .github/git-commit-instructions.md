commit message should be in the format of:

```
<type>(<scope>): <subject>
```

Where:
`<type>`: is the type of the commit, including:
- `feat`: a new feature
- `fix`: a bug fix
- `docs`: documentation only changes
- `style`: changes that do not affect the meaning of the code (white-space, formatting
- `refactor`: a code change that neither fixes a bug nor adds a feature
- `perf`: a code change that improves performance
- `test`: adding missing tests or correcting existing tests
- `chore`: changes to the build process or auxiliary tools and libraries such as documentation generation
- `revert`: reverts a previous commit

`<scope>`: is the scope of the commit, which can be anything specifying the place of the commit change. For example: `ui`, `api`, `database`, etc.

`<subject>`: is a short description of the commit, which should be concise and to the point. It should not exceed 200 characters.

### Language

`<subject>` may be written in Chinese or English:

- **English**: use an English summary directly, e.g. `feat(api): add case execution endpoint`.
- **Chinese**: MUST be accompanied by an English summary, appended as a footer with the `English:` prefix, e.g.:

  ```
  feat(api): 新增用例执行接口

  English: Add case execution endpoint
  ```