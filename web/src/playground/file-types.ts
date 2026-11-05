// extensionMIME 与官网一致：浏览器没有给出类型的文件按扩展名取的 MIME
const extensionMIME: Record<string, string> = {
  csv: 'text/csv',
  txt: 'text/plain',
  html: 'text/html',
  htm: 'text/html',
  css: 'text/css',
  xml: 'text/xml',
  svg: 'image/svg+xml',
  rtf: 'text/rtf',
  pdf: 'application/pdf',
  hwp: 'application/hwp',
  hwpx: 'application/hwp+zip',
  doc: 'application/msword',
  xls: 'application/vnd.ms-excel',
  ppt: 'application/vnd.ms-powerpoint',
  docx: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  dotx: 'application/vnd.openxmlformats-officedocument.wordprocessingml.template',
  xlsx: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  pptx: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
  odt: 'application/vnd.oasis.opendocument.text',
  ods: 'application/vnd.oasis.opendocument.spreadsheet',
  odp: 'application/vnd.oasis.opendocument.presentation',
  ipynb: 'application/x-ipynb+json',
  md: 'text/markdown',
  ts: 'text/x-typescript',
  tsx: 'text/x-typescript',
  js: 'text/javascript',
  jsx: 'text/javascript',
  vue: 'text/html',
  svelte: 'text/html',
  json: 'application/json',
  java: 'text/x-java',
  zip: 'application/zip',
  mov: 'video/quicktime',
  aac: 'audio/aac',
  aif: 'audio/aiff',
  aiff: 'audio/aiff',
  alaw: 'audio/alaw',
  flac: 'audio/flac',
  m4a: 'audio/m4a',
  mp3: 'audio/mp3',
  mulaw: 'audio/mulaw',
  ogg: 'audio/ogg',
  oga: 'audio/ogg',
  opus: 'audio/opus',
  wav: 'audio/wav',
}

// textExtensions 与官网一致：浏览器没有给出类型时作为 text/plain 读取的代码与文本文件扩展名
const textExtensions = new Set(
  'ada adb ansible appcache as ascii ascx asp aspx authors awk bashrc bat bazel build bzl c cbl cc cdf cfc cff cfg cfm changelog class clj cljs cmd cob code_of_conduct codeowners coffee conf config contributors cpp cs csh cshtml csproj css csv cu cuh cxx dart django dockerfile dtl editorconfig ejs elm env erb eslintignore eslintrc eta example exs f f90 for fsproj gemfile gemspec gitattributes gitignore gitkeep go gql gradle gs h haml handlebars hgignore hocon hpp hs htaccess htm html hxx in ini inputrc ipynb ixx java jinja jl js json jsp jsx ksh kt kts less license liquid litcoffee lock log lua m mailcap make makefile manifest markdown md mdown mjs mkdn mustache mxml notice npmrc nse nuspec org oss pas patents pbtxt php php3 php4 php5 pl pm po postcss pot pp prettierrc profile properties proto protoascii ps1 pug puppet py r rake rb rdata readme rkt rs rst rtf rules sass scala scss sh shtml slim sln sql srt styl support svelte svg swift tcl tex texi textproto todo toml ts tsv tsx txt txtpb vala vash vb vbhtml vbproj vbs vimrc vue webmanifest workspace xinitrc xml yaml yml zshrc'.split(
    ' ',
  ),
)

// fileMIME 与官网 _.Bo 一致：取浏览器给出的类型，没有时按扩展名取，代码与文本文件为 text/plain，其余为 application/octet-stream
export function fileMIME(file: File): string {
  const extension = file.name.split('.').pop()!.toLowerCase()
  return (
    file.type ||
    extensionMIME[extension] ||
    (textExtensions.has(extension) ? 'text/plain' : 'application/octet-stream')
  )
}
