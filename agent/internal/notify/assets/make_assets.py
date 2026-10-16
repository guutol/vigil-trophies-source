"""Regenerate the original notification sounds, without overwriting the approved trophy art.

The grade PNGs are shared with web/public/art (see web/docs/brand/ARTWORK.md).
Run with Python 3; sounds use only the standard library.
"""
import math
import struct
import wave
from pathlib import Path

HERE = Path(__file__).resolve().parent
RATE = 44100

def tone(freq, start, length, gain, total):
    """Soft bell: fundamental, quiet partial, gentle attack and exponential decay."""
    out = [0.0] * total
    a = int(start * RATE)
    for i in range(int(length * RATE)):
        if a + i >= total:
            break
        t = i / RATE
        env = min(1.0, t / 0.015) * math.exp(-t * 7.5)
        v = math.sin(2 * math.pi * freq * t) + 0.18 * math.sin(2 * math.pi * freq * 2.0 * t)
        out[a + i] = v * env * gain
    return out

def write_wav(path, notes, seconds):
    total = int(seconds * RATE)
    mix = [0.0] * total
    for freq, start, length, gain in notes:
        for i, value in enumerate(tone(freq, start, length, gain, total)):
            mix[i] += value
    peak = max(abs(v) for v in mix) or 1
    frames = b''.join(struct.pack('<h', int(v / peak * 0.25 * 32767)) for v in mix)
    with wave.open(str(path), 'wb') as sound:
        sound.setnchannels(1)
        sound.setsampwidth(2)
        sound.setframerate(RATE)
        sound.writeframes(frames)

def main():
    write_wav(HERE / 'chime.wav', [(659.3, 0.0, 0.5, 0.8), (987.8, 0.1, 0.6, 0.7)], 0.7)
    write_wav(HERE / 'chime-platinum.wav', [(523.3, 0.0, 0.8, 0.7), (659.3, 0.12, 0.8, 0.7),
                                         (784.0, 0.24, 0.9, 0.7), (1046.5, 0.36, 1.0, 0.8)], 1.35)
    print('Notification sounds exported; trophy artwork unchanged.')

if __name__ == '__main__':
    main()
