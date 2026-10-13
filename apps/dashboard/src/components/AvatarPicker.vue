<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, nextTick, watch } from 'vue';
import Icon from './Icon.vue';
import { Smile, Dog, Hamburger, Volleyball, Car, Lightbulb, Hash } from '@lucide/vue';
import type { EmojiRecord } from '../data/emoji-data';
defineProps<{ modelValue: string }>();
const emit = defineEmits<{ 'update:modelValue': [string]; close: [] }>();
const host = ref<HTMLElement>();
function outside(event: PointerEvent) {
  const target = event.target as HTMLElement;
  if (!host.value?.contains(target) && !target.closest('[aria-label="Change avatar"]'))
    emit('close');
}
onMounted(() => {
  void nextTick(() => host.value?.querySelector<HTMLButtonElement>('button')?.focus());
  window.addEventListener('pointerdown', outside);
});
onUnmounted(() => window.removeEventListener('pointerdown', outside));
const expanded = ref(false),
  query = ref(''),
  category = ref('Recent'),
  error = ref(''),
  file = ref<HTMLInputElement>();
const quick = [
  '🐙',
  '🦊',
  '🦉',
  '🐝',
  '🐼',
  '🐸',
  '🐯',
  '🦁',
  '🐨',
  '🐵',
  '🐧',
  '🐳',
  '🦋',
  '🌞',
  '🌙',
  '⭐',
  '🔥',
  '⚡',
  '🍀',
  '🌈',
  '🚀',
  '🤖',
  '👾',
  '🧠',
];
const groups = [
  { name: 'Recent', icon: 'clock', label: 'Frequently used', ids: [] },
  { name: 'People', symbol: Smile, label: 'Smileys & People', ids: [0, 1] },
  { name: 'Nature', symbol: Dog, label: 'Animals & Nature', ids: [3] },
  { name: 'Food', symbol: Hamburger, label: 'Food & Drink', ids: [4] },
  { name: 'Activities', symbol: Volleyball, label: 'Activities', ids: [6] },
  { name: 'Travel', symbol: Car, label: 'Travel & Places', ids: [5] },
  { name: 'Objects', symbol: Lightbulb, label: 'Objects', ids: [7] },
  { name: 'Symbols', symbol: Hash, label: 'Symbols', ids: [8] },
  { name: 'Flags', icon: 'flag', label: 'Flags', ids: [9] },
];
const catalog = ref<EmojiRecord[]>([]),
  tone = ref(0),
  tonesOpen = ref(false);
const toneNames = ['Default', 'Light', 'Medium-light', 'Medium', 'Medium-dark', 'Dark'];
const toneColors = ['#ffcd38', '#f7dece', '#f3d2a2', '#d5ab88', '#ad7858', '#6c4a37'];
const recent = ['👍', '😀', '😘', '😍', '😆', '😜', '😅', '😂', '🌍', '😱'];
watch(expanded, async (value) => {
  if (value && !catalog.value.length) catalog.value = (await import('../data/emoji-data')).default;
});
const normalized = (s: string) => s.replace(/\ufe0f/g, '');
const visible = computed(() => {
  const q = query.value.trim().toLowerCase();
  if (q)
    return [
      {
        name: 'Results',
        label: 'Search results',
        items: catalog.value.filter((e) =>
          (e.label + ' ' + e.keywords + ' ' + e.emoji).toLowerCase().includes(q),
        ),
      },
    ];
  return (
    category.value === 'Recent'
      ? groups.slice(0, 2)
      : groups.filter((g) => g.name === category.value)
  ).map((g) => ({
    ...g,
    items:
      g.name === 'Recent'
        ? recent
            .map((emoji) => catalog.value.find((e) => normalized(e.emoji) === emoji))
            .filter((e): e is EmojiRecord => !!e)
        : catalog.value.filter((e) => g.ids.includes(e.group)),
  }));
});
const rendered = (emoji: EmojiRecord) =>
  emoji.skins.find((s) => s[0] === tone.value)?.[1] ?? emoji.emoji;
function choose(value: string) {
  emit('update:modelValue', value);
  emit('close');
}
async function upload(event: Event) {
  const image = (event.target as HTMLInputElement).files?.[0];
  if (!image) return;
  if (
    !['image/png', 'image/jpeg', 'image/webp', 'image/gif'].includes(image.type) ||
    image.size > 2_000_000
  ) {
    error.value = 'Choose a PNG, JPEG, WebP or GIF smaller than 2 MB.';
    return;
  }
  const reader = new FileReader();
  reader.onload = () => choose(String(reader.result));
  reader.onerror = () => {
    error.value = 'Unable to read image.';
  };
  reader.readAsDataURL(image);
}
</script>
<template>
  <div
    ref="host"
    class="avatar-picker"
    :class="{ expanded }"
    role="dialog"
    aria-label="Choose avatar"
    @keydown.esc.stop.prevent="emit('close')"
  >
    <template v-if="!expanded"
      ><button type="button" class="upload" @click="file?.click()">
        <Icon name="upload" />Upload image</button
      ><small>Emoji</small>
      <div class="quick-emojis">
        <button
          v-for="emoji in quick"
          :key="emoji"
          type="button"
          :aria-label="emoji"
          :aria-pressed="modelValue === emoji"
          @click="choose(emoji)"
        >
          {{ emoji }}
        </button>
      </div>
      <button type="button" class="more" @click="expanded = true">More emojis…</button></template
    >
    <template v-else
      ><nav aria-label="Emoji categories">
        <button
          v-for="group in groups"
          :key="group.name"
          type="button"
          :aria-label="group.name"
          :class="{ selected: category === group.name }"
          @click="
            category = group.name;
            query = '';
          "
        >
          <component :is="group.symbol" v-if="group.symbol" :size="19" :stroke-width="1.7" /><Icon
            v-else
            :name="group.icon!"
            :size="19"
          />
        </button>
      </nav>
      <div class="emoji-search">
        <input v-model="query" aria-label="Search emojis" placeholder="Search" /><button
          type="button"
          class="tone-trigger"
          aria-label="Skin tone"
          :aria-expanded="tonesOpen"
          :style="{ background: toneColors[tone] }"
          @click="tonesOpen = !tonesOpen"
        />
        <div v-if="tonesOpen" class="tones" role="group" aria-label="Skin tones">
          <button
            v-for="(color, index) in toneColors"
            :key="color"
            type="button"
            :aria-label="toneNames[index] + ' skin tone'"
            :aria-pressed="tone === index"
            :style="{ background: color }"
            @click="
              tone = index;
              tonesOpen = false;
            "
          />
        </div>
      </div>
      <div class="emoji-scroll">
        <section v-for="group in visible" :key="group.name">
          <p>{{ group.label }}</p>
          <div class="emoji-grid">
            <button
              v-for="emoji in group.items"
              :key="emoji.emoji"
              type="button"
              :aria-label="emoji.label"
              :title="emoji.label"
              @click="choose(rendered(emoji))"
            >
              {{ rendered(emoji) }}
            </button>
          </div>
        </section>
        <p v-if="catalog.length && !visible.some((group) => group.items.length)">No emojis found</p>
      </div></template
    >
    <input
      ref="file"
      hidden
      type="file"
      accept="image/png,image/jpeg,image/webp,image/gif"
      @change="upload"
    />
    <p v-if="error" role="alert">{{ error }}</p>
  </div>
</template>
<style scoped>
.avatar-picker {
  position: absolute;
  left: 0;
  top: 60px;
  z-index: 30;
  width: 288px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--modal);
  box-shadow: 0 8px 25px #0002;
  overflow: hidden;
  font-weight: 400;
  color: var(--text);
}
.upload {
  height: 40px;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 10px;
  width: 100%;
  text-align: left;
  border-bottom: 1px solid var(--border);
  font-size: 14px;
}
.avatar-picker > small {
  display: block;
  font-size: 12px;
  color: var(--muted);
  padding: 8px 9px 4px;
}
.quick-emojis {
  display: grid;
  grid-template-columns: repeat(8, 1fr);
  padding: 0 2px;
}
.quick-emojis button {
  height: 34px;
  font-size: 20px;
  padding: 0;
  border-radius: 4px;
}
.quick-emojis button:hover {
  background: var(--hover);
}
.more {
  width: 100%;
  height: 30px;
  font-size: 12px;
  color: var(--muted);
}
.expanded {
  width: 352px;
  height: 436px;
  background: #171717;
  color: #ddd;
}
.expanded nav {
  display: flex;
  padding: 0 10px;
  height: 43px;
  border-bottom: 1px solid #aaa;
}
.expanded nav button {
  width: 36px;
  padding: 0;
  color: #999;
  border-bottom: 3px solid transparent;
}
.expanded nav button.selected {
  color: #3987fa;
  border-bottom-color: #3987fa;
}
.emoji-search {
  position: relative;
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 9px 11px 4px;
}
.emoji-search input {
  height: 37px;
  background: #ebebeb;
  color: #222;
  border: 0;
  border-radius: 10px;
  padding: 8px 12px;
  width: 292px;
  min-width: 0;
  font-size: 14px;
}
.tone-trigger {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  padding: 0;
  flex: none;
  border: 1px solid #555;
}
.tones {
  position: absolute;
  right: 10px;
  top: 45px;
  background: #292929;
  padding: 8px;
  display: flex;
  gap: 8px;
  border-radius: 6px;
  box-shadow: 0 3px 10px #0008;
  z-index: 1;
}
.tones button {
  width: 22px;
  height: 22px;
  border-radius: 50%;
  padding: 0;
  border: 1px solid #888;
}
.tones button[aria-pressed='true'] {
  outline: 2px solid #3987fa;
}
.emoji-scroll {
  height: 337px;
  overflow: auto;
  scrollbar-width: thin;
  padding: 0 11px 10px;
}
.emoji-scroll p {
  font-size: 14px;
  margin: 3px 5px 6px;
}
.emoji-grid {
  display: grid;
  grid-template-columns: repeat(9, 1fr);
  margin-bottom: 9px;
}
.emoji-grid button {
  height: 36px;
  padding: 0;
  font-size: 22px;
  border-radius: 4px;
}
.emoji-grid button:hover {
  background: #444;
}
.avatar-picker > p {
  font-size: 12px;
  padding: 8px;
}
input[hidden] {
  display: none !important;
}
</style>
