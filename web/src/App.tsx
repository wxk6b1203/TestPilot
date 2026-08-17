import { Navigate, RouterProvider, createHashRouter, useSearchParams } from 'react-router-dom'
import { getToken, setToken } from './api'
import Layout from './pages/Layout'
import Login from './pages/Login'
import Projects from './pages/Projects'
import Environments from './pages/Environments'
import Apis from './pages/Apis'
import GrpcApis from './pages/GrpcApis'
import Cases from './pages/Cases'
import Suites from './pages/Suites'
import Scripts from './pages/Scripts'
import Certificates from './pages/Certificates'
import Plans from './pages/Plans'
import Runs from './pages/Runs'
import Stress from './pages/Stress'
import Workers from './pages/Workers'
import Copilot from './pages/Copilot'
import AdminConsole from './pages/admin/AdminConsole'

function Guard({ children }: { children: React.ReactElement }) {
  return getToken() ? children : <Navigate to="/login" replace />
}

// OIDC/OAuth2 回调落点：后端 302 回跳 #/auth/callback?token=…
function AuthCallback() {
  const [params] = useSearchParams()
  const token = params.get('token')
  if (token) {
    setToken(token)
    return <Navigate to="/apis" replace />
  }
  return (
    <div style={{ padding: 48, textAlign: 'center' }}>
      登录回调缺少 token，<a href="#/login">返回登录</a>
    </div>
  )
}

// data router（createHashRouter）：useBlocker（编辑器未保存离开守卫）要求 data router，
// 组件式 <HashRouter> 不满足。
const router = createHashRouter([
  { path: '/login', element: <Login /> },
  { path: '/auth/callback', element: <AuthCallback /> },
  {
    path: '/',
    element: <Guard><Layout /></Guard>,
    children: [
      { index: true, element: <Navigate to="/apis" replace /> },
      { path: 'apis', element: <Apis /> },
      { path: 'apis/:id', element: <Apis /> },
      { path: 'grpc', element: <GrpcApis /> },
      { path: 'cases', element: <Cases /> },
      { path: 'cases/new', element: <Cases /> },
      { path: 'cases/:id/edit', element: <Cases /> },
      { path: 'suites', element: <Suites /> },
      { path: 'suites/:id/edit', element: <Suites /> },
      { path: 'scripts', element: <Scripts /> },
      { path: 'scripts/:id/edit', element: <Scripts /> },
      { path: 'certs', element: <Certificates /> },
      { path: 'plans', element: <Plans /> },
      { path: 'plans/:id/edit', element: <Plans /> },
      { path: 'runs', element: <Runs /> },
      { path: 'stress', element: <Stress /> },
      { path: 'envs', element: <Environments /> },
      { path: 'projects', element: <Projects /> },
      { path: 'admin', element: <AdminConsole /> },
      { path: 'workers', element: <Workers /> },
      { path: 'copilot', element: <Copilot /> },
    ],
  },
])

export default function App() {
  return <RouterProvider router={router} />
}
