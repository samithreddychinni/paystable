import { NavLink } from 'react-router-dom'
import { Activity, ArrowLeftRight, Zap, LayoutDashboard, Settings } from 'lucide-react'
import { cn } from '../../lib/utils'

const navItems = [
  { to: '/overview',     label: 'Overview',      icon: LayoutDashboard },
  { to: '/health',       label: 'Health',        icon: Activity },
  { to: '/transactions', label: 'Transactions',  icon: ArrowLeftRight },
  { to: '/mismatches',   label: 'Review queue',  icon: Zap },
  { to: '/config',       label: 'Config',        icon: Settings },
]

export default function Sidebar() {
  return (
    <aside className="fixed left-0 top-0 h-screen w-[200px] bg-bg-base border-r border-bg-border flex flex-col z-30">
      <div className="px-4 py-5 border-b border-bg-border">
        <span className="text-text-primary font-medium text-base tracking-tight">paystable</span>
        <span className="text-xs text-text-muted font-mono mt-0.5 block">ops</span>
      </div>

      <nav className="flex-1 py-3 px-2">
        <ul className="space-y-0.5">
          {navItems.map(({ to, label, icon: Icon }) => (
            <li key={to}>
              <NavLink
                to={to}
                className={({ isActive }) =>
                  cn(
                    'flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm transition-colors duration-100',
                    isActive
                      ? 'bg-bg-elevated text-text-primary font-medium'
                      : 'text-text-secondary hover:bg-bg-elevated hover:text-text-primary'
                  )
                }
              >
                <Icon size={16} strokeWidth={1.5} />
                {label}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>

      <div className="px-4 py-4 border-t border-bg-border">
        <div className="flex items-center gap-2">
          <span className="h-1.5 w-1.5 rounded-full bg-status-green animate-pulse" />
          <span className="text-xs text-text-muted">localhost only</span>
        </div>
      </div>
    </aside>
  )
}
