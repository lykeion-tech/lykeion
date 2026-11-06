import { readMassXml } from './mass-xml';
self.onmessage = async (event: MessageEvent<{ text: string; extension: string }>) => {
  const result = await readMassXml(event.data.text, event.data.extension);
  self.postMessage(result ?? null);
};
