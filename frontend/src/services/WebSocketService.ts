import { useEffect, useState } from 'react';

interface WebSocketServiceProps {
  onMessage: (data: any) => void;
  onOpen?: (event: Event) => void;
  onClose?: (event: CloseEvent) => void;
  onError?: (event: Event) => void;
}

class WebSocketService {
  private ws: WebSocket | null = null;

  constructor(url: string, { onMessage, onOpen, onClose, onError }: WebSocketServiceProps) {
    this.ws = new WebSocket(url);

    this.ws.onopen = (event) => {
      if (onOpen) {
        onOpen(event);
      }
    };

    this.ws.onmessage = (event) => {
      const data = JSON.parse(event.data);
      onMessage(data);
    };

    this.ws.onclose = (event) => {
      if (onClose) {
        onClose(event);
      }
    };

    this.ws.onerror = (event) => {
      if (onError) {
        onError(event);
      }
    };
  }

  sendMessage(message: any) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(message));
    }
  }

  close() {
    if (this.ws) {
      this.ws.close();
    }
  }
}

export default WebSocketService;