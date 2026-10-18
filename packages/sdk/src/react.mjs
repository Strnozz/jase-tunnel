'use client';
import { createElement as h, useEffect, useRef } from 'react';
import { mountJaseTunnel } from './widget.mjs';

// The same panel the script tag mounts, inside a React tree.
export function JaseTunnel({ path = '/api/jase-tunnel/v1/' } = {}) {
  const container = useRef(null);
  useEffect(() => {
    const widget = mountJaseTunnel({ path, container: container.current });
    return () => widget.destroy();
  }, [path]);
  return h('div', { ref: container, 'data-jase-tunnel': '' });
}
