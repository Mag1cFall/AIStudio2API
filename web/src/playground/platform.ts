// UserAgentData 是 navigator.userAgentData 中用到的字段
interface UserAgentData {
  mobile: boolean
  platform: string
}

const agent = navigator.userAgent
const agentData = (navigator as Navigator & { userAgentData?: UserAgentData }).userAgentData

// mac 与官网 _.wa 一致：userAgentData 有平台时按平台判断，否则按 UA 中的 Macintosh
export const mac = agentData?.platform
  ? agentData.platform === 'macOS'
  : agent.includes('Macintosh')

// tablet 与官网 _.zn 一致：iPad、Silk，或 UA 中不带 Mobile 的 Android
export const tablet = agentData
  ? !agentData.mobile && /iPad|Android|Silk/.test(agent)
  : /iPad|Silk/.test(agent) || (agent.includes('Android') && !agent.includes('Mobile'))

// phone 与官网 _.An 一致：有 userAgentData 时按 mobile，否则按 UA 中的手机标识且不是平板
export const phone = agentData
  ? agentData.mobile
  : !tablet && /iPod|iPhone|Android|IEMobile/.test(agent)
