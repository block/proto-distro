#include <linux/led-class-multicolor.h>
#include <linux/leds.h>
#include <linux/map_to_7segment.h>
#include <linux/module.h>
#include <linux/mutex.h>
#include <linux/of_device.h>
#include <linux/property.h>
#include <linux/spi/spi.h>

/*
 * Timing as per WS2811 datasheet
 * Line encoding:
 *    Logical 0: high for short time (t0H), low for long time (t0L)
 *    Logical 1: high for long time (t1H), low for long time (t1L)
 *    Logical Reset: low for very long time (tRL)
 *    (t0H, t0L, t1H, t1L; tRL) = (~300ns, ~790ns, ~790ns, ~790ns; >280us)
 *    For simplicity, we round to closest multiple of 400ns:
 *    (t0H, t0L, t1H, t1L; tRL) = (400ns, 800ns, 800ns, 800ns; 281.6us)
 *    Shortest line encoding time is t0H, or 400ns
 *    As expressed in terms of t0H: (t0H, 2*t0H, 2*t0H, 2*t0H, 704*t0H)
 *
 * SPI clk
 *    We need 1 SPI clk cycle to be fast enough for t0H (400ns)
 *    1 cyc / 400ns = 2.5MHz
 *    SPI clk must be 2.5MHz or a multiple of this -> call the multiple 'm'
 *    Then t0H = 1cyc * m
 *        E.g. if spi_clk is 10MHz (m = 4), then t0H = 4cyc
 *
 * SPI output
 *    Recall  (t0H, t0L, t1H, t1L; tRL) = (t0H, 2*t0H, 2*t0H, 2*t0H; 704*t0H);  t0H = 1cyc
 *            (t0H, t0L, t1H, t1L; tRL) = (1cyc, 2cyc, 2cyc, 2cyc; 704cyc)
 *            (t0H, t0L, t1H, t1L; tRL) = (m*1cyc, m*2cyc, m*2cyc, m*2cyc; m*704cyc) if m >= 1
 *    Logical 0:  1cyc high, 2 cyc low (multiply all by m if needed)
 *        Looks like: 0b100 for m=1, 0b110000 for m=2, etc.
 *    Logical 1:  2cyc high, 2 cyc low (multiply all by m if needed)
 *        Looks like: 0b1100 for m=1, 0b11110000 for m=2, etc.
 *    Logical Reset: 704cyc low (multiply by m if needed)
 *        Looks like: 0b[703:0] = {0}, 0b[1407:0] = {0}, etc.
 */

// SPI clock must have this minimum speed
#define SPI_CLK_MIN_HZ 2500000  // 2.5MHz

// This multiple depends on clk connected to device (2.5MHz, 5MHz, 7.5MHz... etc.)
#define SPI_CLK_MULT 1  // C3 uses PLL4Q = 2.5MHz
#define SPI_HZ       (SPI_CLK_MIN_HZ * SPI_CLK_MULT)

// Number of physical SPI bits (clk cyc) for each logical bit
#define LOGICAL_0_HI_NUM_SPI_BITS  (1 * SPI_CLK_MULT)
#define LOGICAL_0_LO_NUM_SPI_BITS  (2 * SPI_CLK_MULT)
#define LOGICAL_1_HI_NUM_SPI_BITS  (2 * SPI_CLK_MULT)
#define LOGICAL_1_LO_NUM_SPI_BITS  (2 * SPI_CLK_MULT)
#define LOGICAL_RESET_NUM_SPI_BITS (704 * SPI_CLK_MULT)

#define NUM_BITS_ZERO (LOGICAL_0_HI_NUM_SPI_BITS + LOGICAL_0_LO_NUM_SPI_BITS)
#define NUM_BITS_ONE  (LOGICAL_1_HI_NUM_SPI_BITS + LOGICAL_1_LO_NUM_SPI_BITS)

// Since logical 0/1 are of different physical bit sizes
#define MAX(a, b)           (((a) > (b)) ? (a) : (b))
#define MIN(a, b)           (((a) < (b)) ? (a) : (b))
#define MAX_BITS_PER_SYMBOL MAX(NUM_BITS_ZERO, NUM_BITS_ONE)
#define MIN_BITS_PER_SYMBOL MIN(NUM_BITS_ZERO, NUM_BITS_ONE)

// Always 8 logical bits for each LED
#define LOGICAL_BITS_PER_LED (8)
#define MAX_BITS_PER_LED     (MAX(NUM_BITS_ZERO, NUM_BITS_ONE) * LOGICAL_BITS_PER_LED)
#define MAX_BYTES_PER_LED    ((MAX_BITS_PER_LED + 1) / LOGICAL_BITS_PER_LED)

#define MOST_SIGNIFICANT_BIT (0b10000000)

#define MAX_SEGMENTS        (8)  // a, b, c, d, e, f, g, p
#define MAX_BRIGHTNESS      (255)
#define MAX_CHANNEL_PER_LED (3)

#define BIT_SEG8_H 7

#define _SEG8(l, a, b, c, d, e, f, g, h)                                           \
  ((a << BIT_SEG7_A) | (b << BIT_SEG7_B) | (c << BIT_SEG7_C) | (d << BIT_SEG7_D) | \
   (e << BIT_SEG7_E) | (f << BIT_SEG7_F) | (g << BIT_SEG7_G) | (h << BIT_SEG8_H))

SEG7_DEFAULT_MAP(seg7_display_mapping);

struct ws2811_channel {
  u32 led_id;
  u32 led_offset;
  u8 val;
  u8 buf[MAX_BYTES_PER_LED];
  u32 buf_bit_cnt;
};

struct ws2811_led {
  struct led_classdev_mc cdev;  // Multi-color LED class device
  struct mc_subled cdev_channels[MAX_CHANNEL_PER_LED];
  struct ws2811_channel channels[MAX_CHANNEL_PER_LED];
  u32 channel_count;
};

struct ws2811_seg_display {
  struct led_classdev cdev;  // Seven segment class device
  int mapping;
  char value;
  struct ws2811_channel segments[MAX_SEGMENTS];
  int num_segments;
};

struct ws2811_priv {
  struct spi_device* spi;
  struct mutex mut;
  u32 default_color_index[MAX_CHANNEL_PER_LED];
  int default_color_count;

  struct ws2811_led* leds;
  u32 num_leds;

  struct ws2811_seg_display* seg_displays;
  u32 num_seg_displays;

  // Allocated output buffer for writing final data out over spi buffer
  u8* buf;
  size_t buf_len;

  // Ordered pointer to each WS2811 node's led channels
  struct ws2811_channel** channels;
  u32 num_channels;

  // Total number of WS2811 chips used in the chain
  u32 num_nodes;
};

static int ws2811_refresh(struct ws2811_priv* priv);

static void seg_display_update(struct ws2811_seg_display* seg_display) {
  int i;

  // Update record of LED value
  for (i = 0; i < sizeof(seg_display->segments) / sizeof(seg_display->segments[0]); i++) {
    if ((seg_display->mapping & (1 << i)) > 0) {
      seg_display->segments[i].val = seg_display->cdev.brightness;
    } else {
      seg_display->segments[i].val = 0;
    }
  }
}

static ssize_t value_store(struct device* dev, struct device_attribute* value_attr, const char* buf,
                           size_t size) {
  int ret;
  struct led_classdev* led_cdev = dev_get_drvdata(dev);
  struct ws2811_seg_display* seg_display = container_of(led_cdev, struct ws2811_seg_display, cdev);
  struct ws2811_priv* priv = dev_get_drvdata(dev->parent);

  seg_display->value = buf[0];

  ret = map_to_seg7(&seg7_display_mapping, seg_display->value);
  seg_display->mapping = ret > 0 ? ret : 0;

  dev_dbg(dev, "Seg display %c, map to %d, brightness: %d", seg_display->value,
          seg_display->mapping, led_cdev->brightness);
  seg_display_update(seg_display);
  ws2811_refresh(priv);

  return size;
}

static ssize_t value_show(struct device* dev, struct device_attribute* value_attr, char* buf) {
  struct led_classdev* led_cdev = dev_get_drvdata(dev);
  struct ws2811_seg_display* seg_display = container_of(led_cdev, struct ws2811_seg_display, cdev);

  // Only return the single char value
  buf[0] = seg_display->value;
  buf[1] = '\n';

  return sizeof(seg_display->value) + 1;
}
static DEVICE_ATTR_RW(value);

static struct attribute* seg_display_attrs[] = {
  &dev_attr_value.attr,
  NULL,
};
ATTRIBUTE_GROUPS(seg_display);

static int ws2811_set_seg_display_brightness(struct led_classdev* cdev,
                                             enum led_brightness brightness) {
  struct ws2811_seg_display* seg_display = container_of(cdev, struct ws2811_seg_display, cdev);
  struct ws2811_priv* priv = dev_get_drvdata(cdev->dev->parent);

  seg_display_update(seg_display);

  return ws2811_refresh(priv);
}

/*
 * Creates memory buffer for staging SPI writes.
 * Since each WS2811 has to latch 24 logical bits, and each LED is assigned 8 logical bits,
 * we need to round up num_nodes to the nearest multiple of MAX_CHANNEL_PER_LED.
 *
 * Be sure to fill the extra buffer with logical 0 or 1. Otherwise behaviour of
 * the device is undefined.
 *
 * We also allocate based on the longer physical bit representation of either 0 or 1.
 */
static int ws2811_alloc_led_mem(struct device* dev, struct ws2811_priv* priv) {
  priv->buf_len = (LOGICAL_RESET_NUM_SPI_BITS +
                   (priv->num_channels * LOGICAL_BITS_PER_LED * MAX_BITS_PER_SYMBOL)) /
                  8;              // Divide by 8 to get bytes.
  priv->buf_len *= SPI_CLK_MULT;  // In cases where a faster clock is used

  priv->buf = devm_kzalloc(dev, priv->buf_len, GFP_KERNEL);
  if (!priv->buf) {
    dev_err(dev, "Failed buffer len");
    return -ENOMEM;
  }

  priv->channels =
    devm_kcalloc(dev, priv->num_channels, sizeof(struct ws2811_channel*), GFP_KERNEL);
  if (!priv->channels) {
    dev_err(dev, "Failed allocate channels buff");
    return -ENOMEM;
  }

  if (priv->num_seg_displays) {
    priv->seg_displays =
      devm_kcalloc(dev, priv->num_seg_displays, sizeof(*priv->seg_displays), GFP_KERNEL);
    if (!priv->seg_displays) {
      dev_err(dev, "Failed allocate seg display");
      return -ENOMEM;
    }
  }

  if (priv->num_leds) {
    priv->leds = devm_kcalloc(dev, priv->num_leds, sizeof(*priv->leds), GFP_KERNEL);
    if (!priv->leds) {
      dev_err(dev, "Failed allocate singles");
      return -ENOMEM;
    }
  }

  return 0;
}

/*
 * Helper function: Append a single bit into a byte buffer.
 * bit_pos is the current bit index in the output stream.
 * Increases rel_bit_pos so this function can be called sequentially.
 */
static inline void append_bit(u8* base_addr, u32* rel_bit_pos, int bit_val) {
  int i;
  // Loop takes care of pulse stretching
  for (i = 0; i < SPI_CLK_MULT; i++) {
    u8* byte_addr = base_addr + (*rel_bit_pos / 8);
    int bit_idx = *rel_bit_pos % 8;
    if (bit_val) {
      *byte_addr |= (MOST_SIGNIFICANT_BIT >> bit_idx);
    } else {
      *byte_addr &= ~(MOST_SIGNIFICANT_BIT >> bit_idx);
    }
    // Increment relative bit pointer
    (*rel_bit_pos)++;
  }
}

static int ws2811_refresh(struct ws2811_priv* priv) {
  int i, j, ret;
  u8* byte_addr;
  u32 bit_offset = 0;  // Relative bit position from byte_addr

  mutex_lock(&priv->mut);
  /*
   * Write brightness values for all LEDs.
   * Start at first LED (after Reset region).
   */
  byte_addr = priv->buf + (LOGICAL_RESET_NUM_SPI_BITS / 8);

  for (i = 0; i < priv->num_channels; i++) {
    struct ws2811_channel* channel = priv->channels[i];
    u8 val = 0;
    if (channel) {
      val = channel->val;
    }

    // Encode each bit of brightness (MSb first)
    for (j = LOGICAL_BITS_PER_LED - 1; j >= 0; j--) {
      if (val & (1 << j)) {
        // Logical '1' -> append 0b1100 (4 bits)
        append_bit(byte_addr, &bit_offset, 1);
        append_bit(byte_addr, &bit_offset, 1);
        append_bit(byte_addr, &bit_offset, 0);
        append_bit(byte_addr, &bit_offset, 0);
      } else {
        // Logical '0' -> append 0b100 (3 bits)
        append_bit(byte_addr, &bit_offset, 1);
        append_bit(byte_addr, &bit_offset, 0);
        append_bit(byte_addr, &bit_offset, 0);
      }
    }
  }

  ret = spi_write(priv->spi, priv->buf, priv->buf_len);
  if (ret < 0)
    dev_err(&priv->spi->dev, "SPI write failed: %d\n", ret);

  mutex_unlock(&priv->mut);

  return ret;
}

static int ws2811_set_led(struct led_classdev* cdev, enum led_brightness brightness) {
  int i;
  const struct mc_subled* subled;
  struct led_classdev_mc* mcdev = lcdev_to_mccdev(cdev);
  struct ws2811_priv* priv = dev_get_drvdata(cdev->dev->parent);

  // Update record of LED value
  for (i = 0; i < mcdev->num_colors; i++) {
    subled = &mcdev->subled_info[i];
    priv->channels[subled->channel]->val =
      (cdev->brightness * subled->intensity) / cdev->max_brightness;
  }

  return ws2811_refresh(priv);
}

static int ws2811_parse_dts(struct device* dev, struct ws2811_priv* priv) {
  struct fwnode_handle* child;
  u32 max_node_idx = 0;
  u32 reg[2];

  priv->num_leds = 0;
  priv->num_seg_displays = 0;
  priv->default_color_count = 0;

  priv->default_color_count = fwnode_property_count_u32(dev_fwnode(dev), "default-colors");
  if (priv->default_color_count <= 0) {
    dev_err(dev, "Missing default-colors property!\n");
  } else if (priv->default_color_count > MAX_CHANNEL_PER_LED) {
    dev_err(dev, "Provided %d default-colors. Can't support more than %d!\n",
            priv->default_color_count, MAX_CHANNEL_PER_LED);
  } else {
    if (fwnode_property_read_u32_array(dev_fwnode(dev), "default-colors", priv->default_color_index,
                                       priv->default_color_count)) {
      dev_err(dev, "Failed reading default-colors property!\n");
      priv->default_color_count = 0;
    } else {
      dev_dbg(dev, "Found %d default colors.", priv->default_color_count);
    }
  }

  fwnode_for_each_child_node(dev_fwnode(dev), child) {
    const char* name = fwnode_get_name(child);
    // Check if node is prefixed with 7seg
    if (!strcmp("7seg", fwnode_get_name(child))) {
      struct fwnode_handle* seg_child;

      fwnode_for_each_child_node(child, seg_child) {
        dev_dbg(dev, "- %s", fwnode_get_name(seg_child));

        if (fwnode_property_read_u32_array(seg_child, "reg", reg, 2)) {
          dev_err(dev, "Missing reg property for segment %s\n", fwnode_get_name(seg_child));
          continue;
        }

        if (reg[0] > max_node_idx) {
          max_node_idx = reg[0];
        }
      }
      priv->num_seg_displays++;
    } else if (strstr(name, "led@") == name) {  // prefix with led@
      dev_dbg(dev, "%s", fwnode_get_name(child));

      if (fwnode_property_read_u32_array(child, "reg", reg, 2)) {
        dev_dbg(dev, "Missing reg property for segment %s\n", fwnode_get_name(child));
        continue;
      }

      priv->num_leds++;
      if (reg[0] > max_node_idx) {
        max_node_idx = reg[0];
      }
    }
  }
  priv->num_nodes = max_node_idx + 1;
  priv->num_channels = priv->num_nodes * MAX_CHANNEL_PER_LED;
  return 0;
}

static int ws2811_init_leds(struct device* dev, struct ws2811_priv* priv) {
  struct fwnode_handle* child;
  int ret;
  u32 seg_display_idx = 0;
  u32 led_idx = 0;

  // Override the default 7-segment display mapping with some mapping specific to the 8 segment
  // display we use that include a period.
  seg7_display_mapping.table['.'] = _SEG8('.', 0, 0, 0, 0, 0, 0, 0, 1);
  seg7_display_mapping.table[127] = _SEG8(127, 1, 1, 1, 1, 1, 1, 1, 1);

  fwnode_for_each_child_node(dev_fwnode(dev), child) {
    u32 reg[2];
    const char* label;
    const char* name = fwnode_get_name(child);

    // Get LED's name
    if (fwnode_property_read_string(child, "label", &label)) {
      dev_err(dev, "LED %s: Missing label!\n", name);
      continue;
    }

    // Check if node is prefixed with 7seg
    if (!strcmp("7seg", fwnode_get_name(child))) {
      struct fwnode_handle* seg_child;
      u32 segment_idx = 0;
      struct ws2811_seg_display* seg_display = &priv->seg_displays[seg_display_idx];

      fwnode_for_each_child_node(child, seg_child) {
        struct ws2811_channel* channel = &seg_display->segments[segment_idx];

        if (fwnode_property_read_u32_array(seg_child, "reg", reg, 2)) {
          dev_err(dev, "Missing reg property for segment %s\n", fwnode_get_name(seg_child));
          continue;
        }

        channel->led_id = reg[0];
        channel->led_offset = reg[1];
        segment_idx++;

        priv->channels[reg[0] * MAX_CHANNEL_PER_LED + reg[1]] = channel;
      }

      seg_display->cdev.name = label;
      seg_display->cdev.max_brightness = MAX_BRIGHTNESS;
      seg_display->cdev.brightness = MAX_BRIGHTNESS;
      seg_display->cdev.brightness_set_blocking = ws2811_set_seg_display_brightness;
      seg_display->cdev.groups = seg_display_groups;

      ret = led_classdev_register_ext(dev, &seg_display->cdev, NULL);
      if (ret) {
        dev_err(dev, "Failed to register segment display %s\n", name);
        continue;
      }

      seg_display_idx++;

    } else if (strstr(name, "led@") == name) {  // prefix with led@
      u32 channel_idx, channel_colors[MAX_CHANNEL_PER_LED];
      int i, channel_count;
      struct ws2811_channel* chan;

      // Get LED ID and offset value
      if (fwnode_property_read_u32_array(child, "reg", reg, 2)) {
        dev_err(dev, "Missing register addr %s\n", name);
        continue;
      }

      // Figure out the start of channel index
      channel_idx = reg[0] * MAX_CHANNEL_PER_LED + reg[1];

      channel_count = fwnode_property_count_u32(child, "colors");
      if (channel_count > MAX_CHANNEL_PER_LED) {
        dev_err(dev, "%s provided %d channel colors. Can't support more than %d!\n", name,
                channel_count, MAX_CHANNEL_PER_LED);
        continue;
      }

      // If colors is provided, read it into channel_colors array
      // if colors is NOT provided, try to use the default_color_index
      if (channel_count > 0) {
        if (fwnode_property_read_u32_array(child, "colors", channel_colors, channel_count)) {
          dev_err(dev, "Failed reading colors property for %s\n", name);
          continue;
        }
      } else if ((channel_count <= 0) && (priv->default_color_count > 0)) {
        channel_count = priv->default_color_count;
        for (i = 0; i < channel_count; i++) {
          channel_colors[i] = priv->default_color_index[i];
        }
      } else {
        dev_err(dev, "Missing channel colors for %s!\n", name);
        continue;
      }

      // Init color channels
      for (i = 0; i < channel_count; i++) {
        chan = &priv->leds[led_idx].channels[i];
        chan->led_id = reg[0];
        chan->led_offset = i;

        // Map channel into Ordered channel mapping
        priv->channels[channel_idx + i] = chan;
        priv->leds[led_idx].cdev_channels[i].channel = channel_idx + i;
        priv->leds[led_idx].cdev_channels[i].color_index = channel_colors[i];
      }

      priv->leds[led_idx].cdev.num_colors = channel_count;
      priv->leds[led_idx].cdev.subled_info = priv->leds[led_idx].cdev_channels;
      priv->leds[led_idx].cdev.led_cdev.name = label;
      priv->leds[led_idx].cdev.led_cdev.max_brightness = MAX_BRIGHTNESS;
      priv->leds[led_idx].cdev.led_cdev.brightness_set_blocking = ws2811_set_led;

      ret = led_classdev_multicolor_register_ext(dev, &priv->leds[led_idx].cdev, NULL);
      if (ret) {
        dev_err(dev, "Failed to register RGB LED %s\n", name);
        continue;
      }

      led_idx++;
    }
  }
  return 0;
}

/*
 * This function is used to write logical 0s (not 0b0) to the data buffer
 * after the Reset region. The ws2811_set_single_brightness() function conducts SPI
 * writes.
 * NOTE: The Reset region should be always be 0b000..0.
 */
static int ws2811_clear_all_leds(struct ws2811_priv* priv) {
  u32 i;

  // Clear all internally managed LED data. Write 0 brightness to each LED.
  for (i = 0; i < priv->num_channels; i++) {
    if (priv->channels[i])
      priv->channels[i]->val = 0;
  }

  return ws2811_refresh(priv);
}

// Function that is run when the device is detected
static int miner_io_led_probe(struct spi_device* spi) {
  struct device* dev = &spi->dev;
  struct ws2811_priv* priv = NULL;

  int ret;
  dev_info(&spi->dev, "Miner IO LED SPI driver probed\n");
  dev_info(&spi->dev, "Expecting SPI clk of %dHz\n", SPI_HZ);

  // Allocate memory for this device
  priv = devm_kzalloc(dev, sizeof(*priv), GFP_KERNEL);
  if (!priv)
    return -ENOMEM;

  ret = ws2811_parse_dts(dev, priv);
  if (ret)
    return -EINVAL;
  dev_info(dev, "Initializing WS2811: %d channels %d nodes.\n", priv->num_channels,
           priv->num_nodes);

  priv->spi = spi;

  // Important for ensuring internal buffer stays coherent
  mutex_init(&priv->mut);

  ret = ws2811_alloc_led_mem(dev, priv);
  if (ret)
    return ret;

  ret = ws2811_init_leds(dev, priv);
  if (ret)
    return ret;

  spi_set_drvdata(spi, priv);

  return 0;
}

// Function called when device is removed
static int miner_io_led_remove(struct spi_device* spi) {
  int i;
  struct ws2811_priv* priv = spi_get_drvdata(spi);

  if (!priv) {
    dev_info(&spi->dev, "SPI device not found\n");
    return 0;
  }

  dev_info(&spi->dev, "Removing WS2811 LED driver\n");

  for (i = priv->num_leds - 1; i >= 0; i--) led_classdev_multicolor_unregister(&priv->leds[i].cdev);
  /* for (i = priv->num_seg_displays - 1; i >= 0; i--) */
  /*   led_classdev_unregister(&priv->seg_displays[i].cdev); */

  mutex_destroy(&priv->mut);
  // Below are kernel managed, but clean up explicitly
  devm_kfree(&spi->dev, priv->buf);
  if (priv->leds)
    devm_kfree(&spi->dev, priv->leds);
  if (priv->seg_displays)
    devm_kfree(&spi->dev, priv->seg_displays);
  if (priv->channels)
    devm_kfree(&spi->dev, priv->channels);

  return 0;
}

// Define what devices this driver should support
static const struct of_device_id ws2811_dev_tree_ids[] = {
  {.compatible = "worldsemi,ws2811f"},
  {},
};
MODULE_DEVICE_TABLE(of, ws2811_dev_tree_ids);

// A SPI subsystem driver, to link with device tree ids above
static struct spi_driver miner_io_led_driver = {
  .probe = miner_io_led_probe,
  .remove = miner_io_led_remove,
  .driver =
    {
      .name = KBUILD_MODNAME,
      .of_match_table = ws2811_dev_tree_ids,
    },
};

// Register this SPI driver with kernel
module_spi_driver(miner_io_led_driver);

// License must be GPL to use various kernel APIs
MODULE_LICENSE("GPL");
MODULE_AUTHOR("Proto Mining SW Team");
MODULE_DESCRIPTION("Proto Miner IO LED Driver using SPI");
