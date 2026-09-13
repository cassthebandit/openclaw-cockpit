import os, sys, time
path = sys.argv[1]
sys.stdout.write("\033[?1049h")
last = None
def draw():
    global last
    w,h = os.get_terminal_size()
    mode = open(path).read()
    key = (w,h,mode)
    if key == last: return
    last = key
    text = {"idle":"Cooked for 7m 2s", "working":"Thinking… esc to interrupt", "permission":"Permission requested", "background":"Cooked for 7m 2s; still running"}[mode]
    sys.stdout.write("\033[H\033[2J")
    sys.stdout.write("\033[%d;1H%s" % (max(1,h-3),text))
    # A compact native viewport loses the input prompt, like the reported bug.
    if h >= 10: sys.stdout.write("\033[%d;1H❯ " % (h-1))
    sys.stdout.write("\033[%d;1HFRAME-%dx%d-%s" % (h,w,h,mode))
    sys.stdout.flush()
while True:
    draw()
    time.sleep(0.01)
