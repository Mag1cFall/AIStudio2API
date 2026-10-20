// SchemaType 是可视化编辑器中属性的类型，enum 写为带 enum 的 string
export type SchemaType = 'string' | 'number' | 'integer' | 'boolean' | 'object' | 'enum'

// schemaTypes 是可视化编辑器可选的属性类型
export const schemaTypes: SchemaType[] = [
  'string',
  'number',
  'integer',
  'boolean',
  'object',
  'enum',
]

// SchemaEntry 是可视化编辑器中的一个属性；extra 保留编辑器不显示的 schema 字段
export interface SchemaEntry {
  key: string
  type: SchemaType
  isArray?: boolean
  isRequired: boolean
  children?: SchemaEntry[]
  values?: string[]
  extra?: Record<string, unknown>
}

// SchemaTree 是可视化编辑器的根对象
export interface SchemaTree {
  children: SchemaEntry[]
  extra?: Record<string, unknown>
}

// JSONSchema 是编辑器读写的 OpenAPI schema 片段
interface JSONSchema {
  type?: string
  enum?: string[]
  items?: JSONSchema
  properties?: Record<string, JSONSchema>
  propertyOrdering?: string[]
  required?: string[]
  [key: string]: unknown
}

// structuralKeys 是由编辑器结构生成的字段，其余字段原样保留
const structuralKeys = new Set([
  'type',
  'enum',
  'items',
  'properties',
  'propertyOrdering',
  'required',
])

// extras 返回 schema 中编辑器不显示的字段
function extras(schema: JSONSchema): Record<string, unknown> | undefined {
  const result = Object.fromEntries(
    Object.entries(schema).filter(([key]) => !structuralKeys.has(key)),
  )
  return Object.keys(result).length > 0 ? result : undefined
}

// entries 按 propertyOrdering 或属性顺序读出对象的属性
function entries(schema: JSONSchema): SchemaEntry[] {
  const properties = schema.properties ?? {}
  const keys = schema.propertyOrdering?.length ? schema.propertyOrdering : Object.keys(properties)
  const required = new Set(schema.required ?? [])
  return keys.map((key) => entry(key, properties[key] ?? {}, required.has(key)))
}

// entry 把一个属性的 schema 读为编辑器条目
function entry(key: string, schema: JSONSchema, isRequired: boolean): SchemaEntry {
  const extra = extras(schema)
  const base = { key, isRequired, ...(extra ? { extra } : {}) }
  const source = schema.items ?? schema
  const isArray = schema.items !== undefined
  if (source.enum?.length)
    return { ...base, type: 'enum', values: [...source.enum], ...(isArray ? { isArray } : {}) }
  return {
    ...base,
    type: (source.type ?? 'string') as SchemaType,
    ...(isArray ? { isArray } : {}),
    ...(source.properties ? { children: entries(source) } : {}),
  }
}

// parseSchema 把 schema JSON 读为编辑器根对象，空文本为空对象
export function parseSchema(text: string): SchemaTree {
  if (text.trim() === '') return { children: [] }
  const value: unknown = JSON.parse(text)
  if (typeof value !== 'object' || value === null || Array.isArray(value))
    throw new Error('Schema must be a JSON object')
  const schema = value as JSONSchema
  if (schema.type && schema.type !== 'object') throw new Error('Schema type must be object')
  const extra = extras(schema)
  return { children: schema.properties ? entries(schema) : [], ...(extra ? { extra } : {}) }
}

// node 把条目写为不含数组包装的 schema
function node(item: SchemaEntry): JSONSchema {
  if (item.type === 'enum') return { type: 'string', enum: item.values ?? [] }
  const result: JSONSchema = { type: item.type }
  if (item.children) Object.assign(result, objectBody(item.children))
  return result
}

// objectBody 写出对象的 properties、propertyOrdering 与 required
function objectBody(children: SchemaEntry[]): JSONSchema {
  const required = children.filter((item) => item.isRequired).map((item) => item.key)
  return {
    properties: Object.fromEntries(
      children.map((item) => {
        const schema = item.isArray ? { type: 'array', items: node(item) } : node(item)
        return [item.key, { ...schema, ...item.extra }]
      }),
    ),
    propertyOrdering: children.map((item) => item.key),
    ...(required.length > 0 ? { required } : {}),
  }
}

// schemaText 把编辑器根对象写为 schema JSON，没有内容时为空文本
export function schemaText(tree: SchemaTree): string {
  if (tree.children.length === 0 && !tree.extra) return ''
  const schema =
    tree.children.length === 0
      ? { ...tree.extra }
      : { type: 'object', ...objectBody(tree.children), ...tree.extra }
  return JSON.stringify(schema, null, 2)
}

// schemaIssue 返回属性名为空或同一层重复时的说明，没有问题时为空文本
export function schemaIssue(children: SchemaEntry[]): string {
  const keys = new Set<string>()
  for (const item of children) {
    if (item.key.trim() === '') return 'Property name must be specified'
    if (keys.has(item.key)) return `Duplicate property "${item.key}"`
    keys.add(item.key)
    const nested = schemaIssue(item.children ?? [])
    if (nested !== '') return nested
  }
  return ''
}

// newEntry 是新增属性的初始值：必填字符串
export function newEntry(): SchemaEntry {
  return { key: '', type: 'string', isRequired: true }
}

// FunctionDeclaration 是函数声明可视化编辑器中的一项，parameters 为 schema JSON 文本，未添加参数时为 undefined
export interface FunctionDeclaration {
  name: string
  description: string
  parameters?: string
  extra?: Record<string, unknown>
}

// functionBehaviors 是函数声明 behavior 的有效取值，不区分大小写
const functionBehaviors = new Set(['BLOCKING', 'NON_BLOCKING'])

// parseFunctions 把函数声明 JSON 数组读为编辑器条目
export function parseFunctions(text: string): FunctionDeclaration[] {
  if (text.trim() === '') return []
  const value: unknown = JSON.parse(text)
  if (!Array.isArray(value)) throw new Error('Function declarations must be a JSON array')
  return value.map((item: unknown) => {
    if (typeof item !== 'object' || item === null || Array.isArray(item))
      throw new Error('Function declaration must be an object')
    const { name, description, parameters, ...extra } = item as Record<string, unknown>
    if (typeof name !== 'string') throw new Error('"name" string must be specified')
    if (typeof description !== 'string') throw new Error('"description" string must be specified')
    if (
      extra.behavior !== undefined &&
      !(typeof extra.behavior === 'string' && functionBehaviors.has(extra.behavior.toUpperCase()))
    )
      throw new Error(`Invalid behavior: ${JSON.stringify(extra.behavior)}`)
    return {
      name,
      description,
      ...(parameters === undefined ? {} : { parameters: JSON.stringify(parameters, null, 2) }),
      ...(Object.keys(extra).length > 0 ? { extra } : {}),
    }
  })
}

// withPropertyOrdering 为缺少 propertyOrdering 的对象 schema 按属性顺序补上，嵌套属性与数组元素同样处理
function withPropertyOrdering(schema: JSONSchema): JSONSchema {
  const result: JSONSchema = { ...schema }
  if (schema.items) result.items = withPropertyOrdering(schema.items)
  if (schema.properties) {
    result.properties = Object.fromEntries(
      Object.entries(schema.properties).map(([key, value]) => [key, withPropertyOrdering(value)]),
    )
    if (!schema.propertyOrdering && Object.keys(schema.properties).length > 0)
      result.propertyOrdering = Object.keys(schema.properties)
  }
  return result
}

// orderedFunctionsText 为函数声明参数中的对象 schema 补上 propertyOrdering 后写回 JSON 数组，与官网保存后发送的声明一致
export function orderedFunctionsText(text: string): string {
  if (text.trim() === '') return ''
  const declarations = JSON.parse(text) as Record<string, unknown>[]
  return JSON.stringify(
    declarations.map((item) =>
      item.parameters
        ? { ...item, parameters: withPropertyOrdering(item.parameters as JSONSchema) }
        : item,
    ),
    null,
    2,
  )
}

// functionsText 把函数声明写为 JSON 数组，名称与描述为必填
export function functionsText(declarations: FunctionDeclaration[]): string {
  if (declarations.length === 0) return ''
  return JSON.stringify(
    declarations.map((item) => {
      if (item.name === '') throw new Error('"name" must be specified')
      if (item.description === '') throw new Error('"description" must be specified')
      return {
        name: item.name,
        description: item.description,
        ...(item.parameters ? { parameters: JSON.parse(item.parameters) as unknown } : {}),
        ...item.extra,
      }
    }),
    null,
    2,
  )
}
