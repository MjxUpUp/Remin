#!/usr/bin/env node
'use strict';
// @reminmem/remin 入口：npm 只负责获取二进制（esbuild 式平台包模式）。
// 运行时直接 spawn 平台包内的原生二进制；落位后（remin doctor --install）
// agent 接线指向 ~/.remin/bin/remin，本 launcher 只在渠道内短命存活。
const { spawn } = require('child_process');
const pkg = require('../package.json');

const platformKey = `${process.platform}-${process.arch}`;
const depName = `@reminmem/remin-${platformKey}`;

if (!pkg.optionalDependencies || !pkg.optionalDependencies[depName]) {
  console.error(`remin: 不支持的平台 ${platformKey}（支持 darwin-arm64/x64、linux-arm64/x64、win32-x64）`);
  process.exit(1);
}

const exe = process.platform === 'win32' ? 'remin.exe' : 'remin';
let bin;
try {
  bin = require.resolve(`${depName}/bin/${exe}`);
} catch (e) {
  console.error(`remin: 平台包 ${depName} 未安装（npm 可能跳过了 optionalDependencies）。`);
  console.error('      请重新安装：npm install -g @reminmem/remin --force');
  process.exit(1);
}

const child = spawn(bin, process.argv.slice(2), { stdio: 'inherit', windowsHide: true });
child.on('error', (err) => {
  console.error('remin: 启动失败:', err.message);
  process.exit(1);
});
child.on('exit', (code, signal) => {
  if (signal) process.kill(process.pid, signal);
  else process.exit(code == null ? 1 : code);
});
