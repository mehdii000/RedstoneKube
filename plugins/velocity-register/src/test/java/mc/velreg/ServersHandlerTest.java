package mc.velreg;

import org.junit.jupiter.api.Test;
import java.util.*;
import static org.junit.jupiter.api.Assertions.*;

class ServersHandlerTest {
  static final class FakeRegistry implements Registry {
    final Map<String,String> servers = new HashMap<>();
    public void register(String name, String address) { servers.put(name, address); }
    public boolean unregister(String name) { return servers.remove(name) != null; }
  }

  @Test void registersOnPost() {
    var reg = new FakeRegistry();
    int code = ServersHandler.handle("POST", "/servers", "Bearer t",
        "{\"name\":\"mg-stub-1\",\"address\":\"10.0.0.5:25565\"}", "t", reg);
    assertEquals(200, code);
    assertEquals("10.0.0.5:25565", reg.servers.get("mg-stub-1"));
  }

  @Test void unregistersOnDelete() {
    var reg = new FakeRegistry();
    reg.register("mg-stub-1", "10.0.0.5:25565");
    int code = ServersHandler.handle("DELETE", "/servers/mg-stub-1", "Bearer t", "", "t", reg);
    assertEquals(200, code);
    assertTrue(reg.servers.isEmpty());
  }

  @Test void rejectsBadToken() {
    var reg = new FakeRegistry();
    int code = ServersHandler.handle("POST", "/servers", "Bearer wrong",
        "{\"name\":\"x\",\"address\":\"y\"}", "t", reg);
    assertEquals(401, code);
    assertTrue(reg.servers.isEmpty());
  }

  @Test void unknownDeleteIs404() {
    int code = ServersHandler.handle("DELETE", "/servers/ghost", "Bearer t", "", "t", new FakeRegistry());
    assertEquals(404, code);
  }
}
