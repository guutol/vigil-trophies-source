"""Compatibility entry point for the SVG-based Windows icon renderer.

Requires the existing web Node dependencies and installed Microsoft Edge.
"""
from pathlib import Path
import subprocess

HERE = Path(__file__).resolve().parent
def main():
    subprocess.run(['node', str(HERE / 'make_icons.cjs')], check=True)

if __name__ == '__main__':
    main()
