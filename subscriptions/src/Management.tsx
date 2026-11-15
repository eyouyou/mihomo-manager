import { useEffect, useState } from 'react'
import { Button, Tabs } from 'antd'
import Subscriptions from './App'
import TrafficRules from './TrafficRules'

function activeTab() {
  return new URLSearchParams(window.location.search).get('view') === 'rules' ? 'rules' : 'subscriptions'
}

export default function Management() {
  const [current, setCurrent] = useState(activeTab)

  useEffect(() => {
    const onLocationChange = () => setCurrent(activeTab())
    window.addEventListener('popstate', onLocationChange)
    return () => window.removeEventListener('popstate', onLocationChange)
  }, [])

  return <>
    <header className="bar">
      <div className="bar-inner">
        <span className="brand">Mihomo Manager</span>
        <Button type="link" href="/ui/">Zashboard</Button>
      </div>
    </header>
    <Tabs className="management-tabs" activeKey={current} onChange={key => {
      const next = key === 'rules' ? '?view=rules' : window.location.pathname
      window.history.pushState(null, '', next)
      setCurrent(key)
    }} items={[
      { key: 'subscriptions', label: '订阅管理', children: <Subscriptions /> },
      { key: 'rules', label: '流量规则', children: <TrafficRules /> },
    ]} />
  </>
}
