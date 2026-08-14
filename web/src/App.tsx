import { HashRouter, Navigate, Route, Routes, useSearchParams } from 'react-router-dom'
import { getToken, setToken } from './api'
import Layout from './pages/Layout'
import Login from './pages/Login'
import Projects from './pages/Projects'
import Environments from './pages/Environments'
import Apis from './pages/Apis'
import ApiDebug from './pages/ApiDebug'
import GrpcApis from './pages/GrpcApis'
import Cases from './pages/Cases'
import CaseEditor from './pages/CaseEditor'
import Suites from './pages/Suites'
import Scripts from './pages/Scripts'
import Plans from './pages/Plans'
import PlanEditor from './pages/PlanEditor'
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

export default function App() {
  return (
    <HashRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/auth/callback" element={<AuthCallback />} />
        <Route path="/" element={<Guard><Layout /></Guard>}>
          <Route index element={<Navigate to="/apis" replace />} />
          <Route path="apis" element={<Apis />} />
          <Route path="apis/:id" element={<ApiDebug />} />
          <Route path="grpc" element={<GrpcApis />} />
          <Route path="cases" element={<Cases />} />
          <Route path="cases/new" element={<CaseEditor />} />
          <Route path="cases/:id/edit" element={<CaseEditor />} />
          <Route path="suites" element={<Suites />} />
          <Route path="suites/:id/edit" element={<Suites />} />
          <Route path="scripts" element={<Scripts />} />
          <Route path="scripts/:id/edit" element={<Scripts />} />
          <Route path="plans" element={<Plans />} />
          <Route path="plans/:id/edit" element={<PlanEditor />} />
          <Route path="runs" element={<Runs />} />
          <Route path="stress" element={<Stress />} />
          <Route path="envs" element={<Environments />} />
          <Route path="projects" element={<Projects />} />
          <Route path="admin" element={<AdminConsole />} />
          <Route path="workers" element={<Workers />} />
          <Route path="copilot" element={<Copilot />} />
        </Route>
      </Routes>
    </HashRouter>
  )
}
