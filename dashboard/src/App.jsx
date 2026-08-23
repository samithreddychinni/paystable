import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import Shell from './components/layout/Shell'
import Overview from './pages/Overview'
import Health from './pages/Health'
import Transactions from './pages/Transactions'
import Mismatches from './pages/Mismatches'
import Config from './pages/Config'
import TestKit from './pages/TestKit'

export default function App() {
  return (
    <BrowserRouter basename="/dashboard">
      <Shell>
        <Routes>
          <Route index element={<Navigate to="/overview" replace />} />
          <Route path="/overview" element={<Overview />} />
          <Route path="/health" element={<Health />} />
          <Route path="/transactions" element={<Transactions />} />
          <Route path="/mismatches" element={<Mismatches />} />
          <Route path="/config" element={<Config />} />
          <Route path="/verification" element={<TestKit />} />
        </Routes>
      </Shell>
    </BrowserRouter>
  )
}
