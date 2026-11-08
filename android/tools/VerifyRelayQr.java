import com.google.zxing.*;
import com.google.zxing.common.*;
import java.awt.image.BufferedImage;
import java.nio.file.*;
import java.util.*;
import javax.imageio.ImageIO;

/** Host-side interoperability check: Go's actual QR encoder -> Android's ZXing decoder. */
class VerifyRelayQr {
    public static void main(String[] args) throws Exception {
        Path directory = Path.of(args[0]);
        BufferedImage image = ImageIO.read(directory.resolve("pairing-synthetic.png").toFile());
        int width = image.getWidth(), height = image.getHeight();
        int[] pixels = image.getRGB(0, 0, width, height, null, 0, width);
        BinaryBitmap bitmap = new BinaryBitmap(new HybridBinarizer(new RGBLuminanceSource(width, height, pixels)));
        Result result = new MultiFormatReader().decode(bitmap, Map.of(DecodeHintType.TRY_HARDER, true));
        if (!Files.readString(directory.resolve("pairing-synthetic.txt")).equals(result.getText())) throw new AssertionError("QR payload differs");
        System.out.println("PASS: Go pairing QR decoded by Android ZXing, " + width + " x " + height);
    }
}
