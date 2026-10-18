## Safari connection support

Safari now uses an explicit local connection window when the visitor clicks **Connect my model**. It avoids the direct HTTPS-to-HTTP loopback fetch blocked by WebKit. Requires **Jase Tunnel desktop 0.3.4 or later**. The domain and model still require native confirmation.

The window is bound to the exact opener, website origin and a random channel. Only discovery, receipt polling and pairing are relayed. Management APIs, model inventory and broker credentials remain private. Tickets never appear in the URL. Chrome and Edge retain the existing direct transport.

Call `client.connect()` from the original click before awaited work. Do not add `noopener` to this internal window. Sites with an opener-isolation policy may need their own policy review or a supported direct-transport browser. The SDK never disables browser protections. Safari automatic announcements are disabled; reconnection starts through the website button.

Verified in Safari 26.6.2 from the Jase HTTPS origin through domain verification and the consent request, with an isolated preview app. SDK tests cover window messages, forged senders, blocked/closed windows and cleanup. Native regression tests cover relay isolation and required model consent.
