import type { Model } from '@/types'

// elementSymbols 是第 1 至第 6 周期主族与过渡元素的符号，按原子序数排列，镧系（57–71）不在其中
const elementSymbols = [
  'H',
  'He',
  'Li',
  'Be',
  'B',
  'C',
  'N',
  'O',
  'F',
  'Ne',
  'Na',
  'Mg',
  'Al',
  'Si',
  'P',
  'S',
  'Cl',
  'Ar',
  'K',
  'Ca',
  'Sc',
  'Ti',
  'V',
  'Cr',
  'Mn',
  'Fe',
  'Co',
  'Ni',
  'Cu',
  'Zn',
  'Ga',
  'Ge',
  'As',
  'Se',
  'Br',
  'Kr',
  'Rb',
  'Sr',
  'Y',
  'Zr',
  'Nb',
  'Mo',
  'Tc',
  'Ru',
  'Rh',
  'Pd',
  'Ag',
  'Cd',
  'In',
  'Sn',
  'Sb',
  'Te',
  'I',
  'Xe',
  'Cs',
  'Ba',
  'Hf',
  'Ta',
  'W',
  'Re',
  'Os',
  'Ir',
  'Pt',
  'Au',
  'Hg',
  'Tl',
  'Pb',
  'Bi',
  'Po',
  'At',
  'Rn',
] as const

// ElementSymbol 是周期表中的元素符号
export type ElementSymbol = (typeof elementSymbols)[number]

// ElementFamily 是元素所属的化学分类，决定已发布元素格的配色
export type ElementFamily =
  | 'alkali'
  | 'alkaline'
  | 'transition'
  | 'postTransition'
  | 'metalloid'
  | 'nonmetal'
  | 'halogen'
  | 'noble'

// Element 是周期表中的一格：原子序数、符号、模型 ID 中的元素代号、所在的周期（行）和族（列）与化学分类
export interface Element {
  number: number
  symbol: ElementSymbol
  codename: string
  period: number
  group: number
  family: ElementFamily
}

// lanthanides 是第 6 周期第 3 族的镧系占位格
export const lanthanides = { first: 57, last: 71, period: 6, group: 3 } as const

// periodStarts 是各周期第一个元素的原子序数
const periodStarts = [1, 3, 11, 19, 37, 55, 87]

// elementAt 返回原子序数对应的周期与族：前三个周期的 p 区靠右排列，第 6 周期的镧系之后从第 4 族继续
function elementAt(number: number): { period: number; group: number } {
  const period = periodStarts.findIndex((start) => number < start)
  const offset = number - periodStarts[period - 1]!
  if (period === 1) return { period, group: offset === 0 ? 1 : 18 }
  if (period <= 3) return { period, group: offset < 2 ? offset + 1 : offset + 11 }
  if (period === 6 && number > lanthanides.last) return { period, group: offset - 13 }
  return { period, group: offset + 1 }
}

// nonmetals 与 metalloids 是 p 区中非金属与类金属的原子序数
const nonmetals = [1, 6, 7, 8, 15, 16, 34]
const metalloids = [5, 14, 32, 33, 51, 52]

// familyOf 返回元素的化学分类
function familyOf(number: number, group: number): ElementFamily {
  if (nonmetals.includes(number)) return 'nonmetal'
  if (metalloids.includes(number)) return 'metalloid'
  if (group === 1) return 'alkali'
  if (group === 2) return 'alkaline'
  if (group <= 12) return 'transition'
  if (group === 17) return 'halogen'
  if (group === 18) return 'noble'
  return 'postTransition'
}

// elementCodenames 是 Gemini 4 模型 ID 中的元素代号，即元素的英文名小写，与 elementSymbols 同序
const elementCodenames = [
  'hydrogen',
  'helium',
  'lithium',
  'beryllium',
  'boron',
  'carbon',
  'nitrogen',
  'oxygen',
  'fluorine',
  'neon',
  'sodium',
  'magnesium',
  'aluminum',
  'silicon',
  'phosphorus',
  'sulfur',
  'chlorine',
  'argon',
  'potassium',
  'calcium',
  'scandium',
  'titanium',
  'vanadium',
  'chromium',
  'manganese',
  'iron',
  'cobalt',
  'nickel',
  'copper',
  'zinc',
  'gallium',
  'germanium',
  'arsenic',
  'selenium',
  'bromine',
  'krypton',
  'rubidium',
  'strontium',
  'yttrium',
  'zirconium',
  'niobium',
  'molybdenum',
  'technetium',
  'ruthenium',
  'rhodium',
  'palladium',
  'silver',
  'cadmium',
  'indium',
  'tin',
  'antimony',
  'tellurium',
  'iodine',
  'xenon',
  'cesium',
  'barium',
  'hafnium',
  'tantalum',
  'tungsten',
  'rhenium',
  'osmium',
  'iridium',
  'platinum',
  'gold',
  'mercury',
  'thallium',
  'lead',
  'bismuth',
  'polonium',
  'astatine',
  'radon',
]

// elements 是周期表的全部元素格
export const elements: Element[] = elementSymbols.map((symbol, index) => {
  const number = index < 56 ? index + 1 : index + 16
  const position = elementAt(number)
  return {
    number,
    symbol,
    codename: elementCodenames[index]!,
    ...position,
    family: familyOf(number, position.group),
  }
})

// codenameSymbols 把模型 ID 中的元素代号映射到元素符号
const codenameSymbols = new Map(elements.map((element) => [element.codename, element.symbol]))

// elementModelID 匹配 Gemini 4 的元素代号模型 ID，如 gemini-4-carbon 与 gemini-4-argon-build
const elementModelID = /^gemini-4-([a-z]+)(-|$)/

// elementOfID 返回模型 ID 中的元素代号对应的元素符号，不是 Gemini 4 元素代号模型时为 undefined
export function elementOfID(id: string): ElementSymbol | undefined {
  const codename = elementModelID.exec(id)?.[1]
  return codename === undefined ? undefined : codenameSymbols.get(codename)
}

// releasedElements 从实时目录取出各元素对应的 Gemini 4 模型，同一元素有多个模型时取 ID 为 preferred 的，其次取 ID 最短的
export function releasedElements(
  models: readonly Model[],
  preferred?: string,
): Map<ElementSymbol, Model> {
  const released = new Map<ElementSymbol, Model>()
  for (const model of models) {
    const symbol = elementOfID(model.id)
    if (symbol === undefined) continue
    const current = released.get(symbol)
    if (
      current === undefined ||
      model.id === preferred ||
      (current.id !== preferred && model.id.length < current.id.length)
    )
      released.set(symbol, model)
  }
  return released
}
