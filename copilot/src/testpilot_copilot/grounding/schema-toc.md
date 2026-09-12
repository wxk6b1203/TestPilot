<!-- 由 scripts/gen_grounding.py 生成，勿手改；实体/枚举清单与 proto 同步 -->

## 数据字典目录

- HttpApi: id, tenantId, projectId, method, uri, params, body, headers, cookies, preScripts, postScripts, settings, certificateId
- KeyValue: key, value, description, enabled
- BodySpec: contentType, oneof content(raw|form|binaryRef)
- Assertion: target, path, op, expected
- TestStep: id, type, name, oneof params(apiCall|grpcCall|assertion|setVar|ifStep|loopStep|retryStep|codeBlock|delay|uiAction)
- ApiCallStep: apiId, override, inline
- AssertionStep: assertions
- SetVarStep: key, valueExpr
- IfStep: conditionExpr, thenSteps, elseSteps
- LoopStep: iterator, oneof bounds(count|range), parallel, bodySteps
- RetryStep: bodyStep, maxAttempts, backoff
- DelayStep: duration
- CodeBlockStep: lang, source
- UiActionStep: action, target, value
- DeclarativeCase: steps
- LowCodeCase: oneof script(scriptRef|source), entry, parameters, httpApiRefs, grpcApiRefs
- TestCase: id, tenantId, projectId, type, name, description, oneof definition(declarative|lowcode), tags, createdBy
- TestPlan: id, tenantId, projectId, envId, name, items, concurrency, retryOnFailure, overlapPolicy, scheduleCron, timeout, notifications
- PlanItem: oneof ref(caseId|suiteId), enabled, paramOverrides
- Environment: id, tenantId, projectId, icon, name, description, baseUrl, variables
- Variable: id, tenantId, projectId, environmentId, scope, category, key, value, sensitive, secretRef, description
- TestRun: id, tenantId, planId, envId, status, trigger, triggeredBy, startedAt, finishedAt, summary
- TestCaseResult: id, runId, caseId, status, duration, error
- TestStepResult: id, caseResultId, stepPath, status, duration, request, response, assertions, logs, artifacts, error
- StressTestPlan: id, tenantId, projectId, envId, oneof target(apiId|behaviorCaseId), loadProfile, workerCount, metricsInterval
- LoadProfile: ramp, duration, concurrencyPerWorker
- RampStage: at, target

- 枚举: HttpMethod, StepType, UiAction, AssertionTarget, AssertionOp, TestCaseType, RunStatus, CaseStatus, StepStatus

以上是数据字典目录。字段结构不确定时，用 query_schema(topic="实体名1,实体名2") 按需查询完整定义（支持逗号分隔多个实体；topic 省略返回全量；枚举恒全部返回）。
