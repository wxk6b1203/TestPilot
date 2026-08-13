import { HashRouter, Navigate, Route, Routes } from 'react-router-dom'
import { getToken } from './api'
import Layout from './pages/Layout'
import Login from './pages/Login'
import Projects from './pages/Projects'
import Environments from './pages/Environments'
import Apis from './pages/Apis'
import Cases from './pages/Cases'
import Plans from './pages/Plans'
import Runs from './pages/Runs'
import Stress from './pages/Stress'
import Workers from './pages/Workers'
import Copilot from './pages/Copilot'

function Guard({ children }: { children: React.ReactElement }) {
  return getToken() ? children : <Navigate to="/login" replace />
}

export default function App() {
  return (
    <HashRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/" element={<Guard><Layout /></Guard>}>
          <Route index element={<Navigate to="/runs" replace />} />
          <Route path="projects" element={<Projects />} />
          <Route path="envs" element={<Environments />} />
          <Route path="apis" element={<Apis />} />
          <Route path="cases" element={<Cases />} />
          <Route path="plans" element={<Plans />} />
          <Route path="runs" element={<Runs />} />
          <Route path="stress" element={<Stress />} />
          <Route path="workers" element={<Workers />} />
          <Route path="copilot" element={<Copilot />} />
        </Route>
      </Routes>
    </HashRouter>
  )
}
