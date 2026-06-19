package mc.metrics;

import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.*;

class MetricsJsonTest {
  @Test void buildsParseableJson() {
    String s = MetricsJson.build(19.987, 3.14, 2, 20, 412, 6.2);
    assertTrue(s.contains("\"tps\":19.99"), s);   // 2dp, ROOT locale dot
    assertTrue(s.contains("\"players\":2"), s);
    assertTrue(s.contains("\"maxPlayers\":20"), s);
    assertTrue(s.contains("\"uptimeSec\":412"), s);
    assertTrue(s.contains("\"jvmStartupSec\":6.2"), s);
  }
}
