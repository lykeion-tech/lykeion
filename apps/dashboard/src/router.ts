import {
  createRouter,
  createWebHashHistory,
  type RouteLocationNormalized,
  type RouteRecordRaw,
} from 'vue-router';

// The tasks page is the landing route and loads eagerly, so the first navigation resolves
// without waiting on the network; every other view is fetched on demand.
import TaskBoard from './views/TaskBoard.vue';
import TaskDetail from './views/TaskDetail.vue';
import ResourceView from './views/ResourceView.vue';
import SettingsView from './views/SettingsView.vue';

declare module 'vue-router' {
  interface RouteMeta {
    // Sidebar destination the route belongs to; unset for unknown paths.
    page?: string;
    // Page heading; App falls back to "Tasks" when unset.
    title?: string;
    // Workspace tab label when it differs from the heading.
    tabTitle?: string;
  }
}

function param(route: RouteLocationNormalized, name: string) {
  const value = route.params[name];
  return (Array.isArray(value) ? value[0] : value) || undefined;
}

// ResourceView renders every non-task, non-settings destination, selected by `page`.
function resource(
  path: string,
  page: string,
  title: string,
  extra: { creation?: boolean; manual?: boolean; tabTitle?: string } = {},
): RouteRecordRaw {
  return {
    path,
    component: ResourceView,
    meta: { page, title, tabTitle: extra.tabTitle },
    props: (route) => ({
      page,
      resourceId: param(route, 'agentId') ?? param(route, 'machineId'),
      runtimeId: param(route, 'runtimeId'),
      creation: extra.creation ?? false,
      manual: extra.manual ?? false,
    }),
  };
}

const create = { creation: true, tabTitle: 'Create Agent' };

const routes: RouteRecordRaw[] = [
  { path: '/', component: TaskBoard, meta: { page: 'tasks', title: 'Tasks' } },
  {
    path: '/tasks/:taskId?',
    components: { default: TaskBoard, detail: TaskDetail },
    meta: { page: 'tasks', title: 'Tasks' },
    props: { default: false, detail: (route) => ({ id: param(route, 'taskId') ?? '' }) },
  },
  {
    path: '/my-tasks',
    component: TaskBoard,
    meta: { page: 'my-tasks' },
    props: { personal: true },
  },
  resource('/inbox', 'inbox', 'Inbox'),
  resource('/chat', 'chat', 'Coresearch'),
  resource('/projects', 'projects', 'Researches'),
  resource('/colleagues', 'colleagues', 'Colleagues'),
  resource('/autoresearch', 'autoresearch', 'Autoresearch'),
  resource('/agents', 'agents', 'Agents'),
  resource('/agents/new', 'agents', 'Agents', create),
  resource('/agents/new/manual', 'agents', 'Agents', { ...create, manual: true }),
  resource('/agents/:agentId', 'agents', 'Agents'),
  resource('/squads', 'squads', 'Teams'),
  resource('/skills', 'skills', 'Skills'),
  resource('/connectors', 'connectors', 'Connectors'),
  resource('/runtimes/:machineId?', 'runtimes', 'Machines'),
  resource('/runtimes/:machineId/runtime/:runtimeId', 'runtimes', 'Machines'),
  resource('/updates', 'updates', 'Updates'),
  {
    path: '/settings/:section?',
    component: SettingsView,
    meta: { page: 'settings', title: 'Settings' },
    props: (route) => ({
      section: String(route.query.tab || param(route, 'section') || 'profile'),
    }),
  },
  // Unknown paths keep the shell's "Tasks" heading over an empty resource page named after
  // their first segment.
  {
    path: '/:pathMatch(.*)*',
    component: ResourceView,
    props: (route) => ({ page: param(route, 'pathMatch') ?? '' }),
  },
];

// Case-sensitive so a miscased path stays an unknown path rather than matching a page.
export const router = createRouter({ history: createWebHashHistory(), routes, sensitive: true });
