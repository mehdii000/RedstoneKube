package mc.velreg;

import com.google.inject.Inject;
import com.velocitypowered.api.event.Subscribe;
import com.velocitypowered.api.event.proxy.ProxyInitializeEvent;
import com.velocitypowered.api.plugin.Plugin;
import com.velocitypowered.api.proxy.ProxyServer;
import com.velocitypowered.api.proxy.server.ServerInfo;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.InputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;

@Plugin(id = "velocity-register", name = "VelocityRegister", version = "0.1.0")
public final class VelRegPlugin {
  private final ProxyServer proxy;

  @Inject public VelRegPlugin(ProxyServer proxy) { this.proxy = proxy; }

  @Subscribe public void onInit(ProxyInitializeEvent e) throws IOException {
    String token = System.getenv("CONTROLLER_TOKEN");

    // Adapt the live proxy to the Registry interface.
    Registry reg = new Registry() {
      public void register(String name, String address) {
        proxy.unregisterServer(new ServerInfo(name, parse(address))); // drop stale, then add (upsert)
        proxy.registerServer(new ServerInfo(name, parse(address)));
      }
      public boolean unregister(String name) {
        return proxy.getServer(name).map(s -> { proxy.unregisterServer(s.getServerInfo()); return true; }).orElse(false);
      }
    };

    HttpServer http = HttpServer.create(new InetSocketAddress("0.0.0.0", 8080), 0);
    http.createContext("/servers", ex -> {
      String body;
      try (InputStream in = ex.getRequestBody()) { body = new String(in.readAllBytes(), StandardCharsets.UTF_8); }
      int code = ServersHandler.handle(ex.getRequestMethod(), ex.getRequestURI().getPath(),
          ex.getRequestHeaders().getFirst("Authorization"), body, token, reg);
      ex.sendResponseHeaders(code, -1);
      ex.close();
    });
    http.start();
  }

  private static InetSocketAddress parse(String hostPort) {
    int i = hostPort.lastIndexOf(':');
    return new InetSocketAddress(hostPort.substring(0, i), Integer.parseInt(hostPort.substring(i + 1)));
  }
}
