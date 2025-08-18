interface WebSocketServiceProps {
  onMessage: (data: unknown) => void;
  onOpen?: (event: Event) => void;
  onClose?: (event: CloseEvent) => void;
  onError?: (event: Event) => void;
}

class WebSocketService {
  private ws: WebSocket | null = null;
  private url: string;
  private props: WebSocketServiceProps;
  private pingTimer: number | null = null;
  private reconnectTimer: number | null = null;

  constructor(url: string, props: WebSocketServiceProps) {
    this.url = url;
    this.props = props;
    this.connect();
  }

  private connect() {
    this.ws = new WebSocket(this.url);

    this.ws.onopen = (event) => {
      this.startPing();
      this.props.onOpen?.(event);
    };

    this.ws.onmessage = (event) => {
      const data = JSON.parse(event.data);
      this.props.onMessage(data);
    };

    this.ws.onclose = (event) => {
      this.stopPing();
      this.props.onClose?.(event);
      this.scheduleReconnect();
    };

    this.ws.onerror = (event) => {
      this.props.onError?.(event);
    };
  }

  private startPing() {
    this.stopPing();
    this.pingTimer = window.setInterval(() => {
      this.sendMessage({ type: 'ping' });
    }, 30000);
  }

  private stopPing() {
    if (this.pingTimer) {
      window.clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) return;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, 1000);
  }

  sendMessage(message: unknown) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(message));
    }
  }

  close() {
    this.stopPing();
    if (this.ws) {
      this.ws.close();
    }
    if (this.reconnectTimer) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }
}

export default WebSocketService;