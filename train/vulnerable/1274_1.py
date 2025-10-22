def sqrt(x):

    if x < 0:
        raise ValueError("Cannot calculate square root of a negative number")
    if x == 0:
        return 0

    guess = x
    next_guess = (guess + 1) // 2

    for _ in range(100):
        guess = next_guess
        next_guess = (guess + x // guess) // 2

    return guess