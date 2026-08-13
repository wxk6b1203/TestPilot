# TestPilot 项目初始说明

> **历史需求文档**：本文是项目启动时的原始需求说明，保留作溯源。其中部分描述已被后续审阅修正
> （如"H2 与 GORM"不兼容、Worker 语言、Copilot 协议等）——**以 `docs/design.md` 为架构准绳**，
> 当前实施进度见 `docs/roadmap.md` 顶部"进度总览"。

## 描述
本项目试图构建一个加入LLM参与的自动化集成测试平台项目，涵盖了HTTP API，GRPC API的能力测试、压力测试，并且支持用Playwright来进行页面E2E测试，涵盖接口管理，自动化测试，文档等功能。并且支持结合Python进行低代码形式的自动化流程测试，最终目的是希望能实现例如OPENAPI/页面的端到端测试。整个项目分为调度器Scheduler，执行器Worker，Copilot，调度器可以由Golang编写，调度器可以由Python或者TS编写（因为Playwright原生支持Python/TS，可以集约到这个服务下），Copilot可以使用PyDantic-AI来作为基础AI框架，用FastAPI暴露Vercel AI V7兼容接口来与Scheduler交互，用来生成测试用例等等相关业务结构，Scheduler同时兼控制台的作用，即与前端进行CRUD。调度器用于对测试相关业务进行CRUD，并且负责分布式调度到目标执行器，执行器用来执行目标测试任务。

## 功能与业务结构
### 基础业务结构
基础业务结构可以完全照抄Postman，Insomnia，Apifox等REST API调试工具的结构，以下会有缺失可以补齐。
#### 项目
用来管理不同的项目，需要记录名称，描述，人员权限（结合RBAC做），以及其他的项目维度的配置

#### 环境
环境是项目维度的，一个项目可以拥有一到多个环境，环境包括
- icon
- 名称
- 描述
- 前置URL
- 环境变量

#### 变量体系
变量体系支持项目为度和环境维度，包括环境变量和参数变量。
在HTTP体系下，参数变量可以为Header，Cookie，Query，Body。Body 类型的参数仅对 form-data 和 x-www-form-urlencoded 形式请求有效。

#### 接口结构
接口区分为HTTP接口/GRPC接口，包含了跟这两种协议相关的可用结构，包括但不限于：
- 方法，如GET/POST等
- URI：基础地址
- Params：请求参数Query
- Body：请求体，Body区分类型Content-Type，如none、form-data、JSON、XML、Binary、GraphQL等
- Cookies：需要注明参数名，参数值，类型，说明等
- 前置操作：脚本，用来跑断言，环境变量设置等
- 后置操作：脚本，用来跑断言，环境变量设置等
- 设置：如TLS证书验证开启与否开关，自动跟随重定向开关，兼容带注释的 JSON等

#### 证书
证书可以用在TLS的各个方面，统一一个位置存储，可以用在项目、接口维度上

#### 目录树
一个项目下的接口展示为目录树结构，每一层都可以包含目录和接口，Coding Agent需要根据这个结构，设计目录树的数据结构。

### 数据存储
数据及其关联存储在Scheduler连接的数据库下，数据库支持PG，MySQL，H2，SQLite，可以考虑使用GORM等ORM框架来存储。

### 导入导出
支持接口导出，项目导出，接口可以导出成 Swagger OPENAPI，curl等，此处的业务逻辑需要完善。

### Vault
支持Hashicorp Vault，Google Tink等。

### Playwright 测试
Worker还需要支持Playwright的测试脚本，用来测试前端。业务结构、结构体等还未定。该功能需要根据开关单独编译。slim：不包含这个功能。

### Python Based 低代码
上述功能即便做好了，也只是精致的Postman轮子，这个功能是为了能够让开发者通过编写Python代码的形式，编写测试用例。这个功能做在Worker上，但是保存在Scheduler上（反正是解释性语言）。因此，要评估一个功能是否可行：对所有基础结构进行Pydantic的封装，并且提供封装接口，用户可以设计自己的调用流程。注意要支持3.13+的asyncio来设计，尽可能都支持异步。类似这样：
```python
# 这里是预先生成好的结构，或者是内置结构
class Response:
    pass
class HttpAPI:
    method: str
    # 其他乱七八糟的字段
    # ...

    # 运行方法，返回
    @abstractmethod
    async def run(self) -> Response:
        pass
    pass
# 允许传入各种参数，或者这个的基础参数可以由其他闭包等方法生成，反正就是传入HTTP API的基础参数等
class CreateUser(HttpAPI):
    """
    这个是对应的CreateUser的API
    """

### 对应于用户的代码，类似于：
req = CreateUser(
    # 各种参数
)
result = await req.run(
    # 这里可能也有各种参数等
)
```
Pydantic 模型可与 ABC 结合，实现“字段验证 + 方法契约”的双重约束。

目的在于，用户可以使用基础类型，跑通自己的测试流程。

### Copilot AI Agent
copilot使用pydantic-ai作为Agent框架，在编译/构建产物的时候，要把DDL等写入到对应的工作目录下，Copilot根据用户的指示来进行生成case，生成接口，或者做其他操作。这里需要完善一下实现。

## 结构划分
### Scheduler
包含了基础交互，数据存储，调度，与Copilot/Worker交互，脚本等通过Scheduler下发下去；与其他二者通过GRPC交互

### Worker
用于实际执行脚本，执行测试计划，执行OPENAPI发起等。

### Copilot
AI Agent，用户生成各种东西