def sqrt_fixed(x: int) -> int:

    if x < 0:
        raise ValueError("Cannot calculate square root of negative number")
    if x == 0:
        return 0

    y = x
    z = (x + 1) // 2
    while z < y:
        y = z
        z = (x // z + z) // 2

    if (y + 1)**2 <= x:
       return y + 1
    else:
       return y