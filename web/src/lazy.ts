// afterTask 在下一个任务中交出已载入的模块，按需载入的块求值与随后的组件挂载分在两个任务里
export function afterTask<T>(module: T): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(module)))
}
