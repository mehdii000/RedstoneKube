package mc.lobby;

import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.*;

class AllocateTest {
  @Test void extractsServerName() {
    assertEquals("mg-stub-abc",
        Allocate.parseServer("{\"server\":\"mg-stub-abc\",\"address\":\"10.0.0.5:25565\"}"));
  }
  @Test void returnsNullWhenAbsent() {
    assertNull(Allocate.parseServer("{\"error\":\"no ready instance\"}"));
  }
}
