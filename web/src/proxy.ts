// maskedProxy 隐藏代理 URL 中的密码
export function maskedProxy(value: string): string {
  return value.replace(/^([a-z][a-z0-9+.-]*:\/\/[^:@/]*):[^@/]*@/i, '$1:•••@')
}
