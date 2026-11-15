// Wires the socket, chat list, compose bar and status line together.
import './theme.css';
import { ChatView } from './chat';
import { Compose } from './compose';
import { DeckSocket, captureSecret, socketUrl, type ConnState } from './socket';
import { StatusBar } from './status';
import type { Status } from './types';

function byId(id: string): HTMLElement {
  const el = document.getElementById(id);
  if (!el) throw new Error(`missing #${id}`);
  return el;
}

// Read the pairing secret from the fragment once, store it, strip it.
captureSecret(window.location, window.localStorage, window.history);

const app = byId('app');
const statusBar = new StatusBar(byId('status-host'), document.body);
let conn: ConnState = 'connecting';
let serviceStatus: Status | null = null;
const renderStatus = () => {
  statusBar.update(conn, serviceStatus);
  document.body.dataset.conn = conn;
};

// `socket` is declared below; these closures only run after it exists.
const compose = new Compose(byId('compose-host'), {
  send: (text) => socket.send(text),
  cancel: (id) => socket.cancel(id),
});
const chat = new ChatView(byId('chat-host'), {
  onTap: (m) => {
    if (m.sender) compose.replyTo(m.sender, m.sender_realm);
  },
});

const socket = new DeckSocket({
  url: socketUrl(window.location),
  storage: window.localStorage,
  handlers: {
    onState: (s) => {
      conn = s;
      if (s === 'not_paired') serviceStatus = null;
      renderStatus();
    },
    onHistory: (messages) => chat.setHistory(messages),
    onMessage: (message) => chat.add(message),
    onStatus: (status) => {
      serviceStatus = status;
      renderStatus();
    },
    onSendResult: (result) => compose.handleResult(result),
  },
});

renderStatus();
socket.start();

// Keep the app inside the visible viewport so the on-screen keyboard never
// covers the newest message or the compose bar (R15).
const vv = window.visualViewport;
let raf = 0;
function fitViewport(): void {
  const height = vv ? vv.height : window.innerHeight;
  const top = vv ? vv.offsetTop : 0;
  app.style.setProperty('--app-h', `${Math.round(height)}px`);
  app.style.setProperty('--app-top', `${Math.round(top)}px`);
  cancelAnimationFrame(raf);
  raf = requestAnimationFrame(() => chat.keepBottom());
}
if (vv) {
  vv.addEventListener('resize', fitViewport);
  vv.addEventListener('scroll', fitViewport);
} else {
  window.addEventListener('resize', fitViewport);
}
fitViewport();
compose.textarea.addEventListener('focus', () => {
  // Some browsers resize after focus without a visualViewport event.
  window.setTimeout(fitViewport, 300);
});
