import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App as AntApp, ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import Management from './Management'
import './styles.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ConfigProvider locale={zhCN} theme={{ token: { colorPrimary: '#234bc9', borderRadius: 10 } }}>
      <AntApp><Management /></AntApp>
    </ConfigProvider>
  </StrictMode>,
)
