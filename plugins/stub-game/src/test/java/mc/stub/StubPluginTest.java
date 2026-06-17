package mc.stub;

import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.*;

class StubPluginTest {
  @Test void buildsDoneUrl() {
    assertEquals("http://controller.mc.svc.cluster.local:8080/instances/mg-stub-abc/done",
        Done.doneUrl("http://controller.mc.svc.cluster.local:8080", "mg-stub-abc"));
  }
  @Test void trimsTrailingSlash() {
    assertEquals("http://c:8080/instances/x/done", Done.doneUrl("http://c:8080/", "x"));
  }
}
