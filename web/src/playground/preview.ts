// previewOrigin 返回 HTML 预览的独立来源：当前页面的协议与主机名加预览监听地址的端口
export function previewOrigin(address: string): string {
  return `${location.protocol}//${location.hostname}:${address.slice(address.lastIndexOf(':') + 1)}`
}
