import { spawn, execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { tmpdir } from 'node:os';
import { describe, expect, it } from 'vitest';

describe.skipIf(process.platform !== 'darwin')('Node to Go inherited pipes', () => {
  it('documents that Go cannot set an inherited stdin deadline on this macOS setup', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lyk-go-stdio-'));
    try {
      const source = path.join(dir, 'main.go');
      const binary = path.join(dir, 'probe');
      writeFileSync(source, `package main
import("encoding/json";"os";"time")
func main(){
 readOK:=os.Stdin.SetReadDeadline(time.Now().Add(3*time.Second))==nil
 writeOK:=os.Stdout.SetWriteDeadline(time.Now().Add(3*time.Second))==nil
 if json.NewEncoder(os.Stdout).Encode(map[string]bool{"read":readOK,"write":writeOK})!=nil {os.Exit(2)}
}`);
      execFileSync('go', ['build', '-o', binary, source], { env: { ...process.env, GOCACHE: path.join(tmpdir(), 'lykeion-desktop-go-cache') }, timeout: 30000 });
      const child = spawn(binary, [], { stdio: ['pipe', 'pipe', 'pipe'] });
      const out: Buffer[] = [];
      child.stdout.on('data', (chunk) => { out.push(Buffer.from(chunk)); });
      child.stdin.end();
      const code = await new Promise<number | null>((resolve, reject) => { child.once('error', reject); child.once('close', resolve); });
      expect(code).toBe(0);
      expect(JSON.parse(Buffer.concat(out).toString()).read).toBe(false);
    } finally { rmSync(dir, { recursive: true, force: true }); }
  }, 35000);
});
