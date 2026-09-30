"""doc with 'quotes' and # hash"""
x = 1  # it's a comment
y = 'a#b' + "c\"d"
z = '''multi
line 'text' # not a comment
'''
def test_adds():
    pass
class T:
    async def test_later(self):
        pass
def helper():
    pass
import os  # noqa
y = 2  # type: ignore
z = 3  # pragma: no cover
