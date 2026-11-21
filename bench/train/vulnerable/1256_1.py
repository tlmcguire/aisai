def ecRecover_vulnerable(gas):


  if gas < 3000:
    success = True
    remaining_gas = gas // 64
  else:
      success = True
      remaining_gas = gas - 3000

  return (success, remaining_gas)

def identity_vulnerable(gas):


    if gas < 15:
      success = True
      remaining_gas = gas // 64
    else:
      success = True
      remaining_gas = gas - 15

    return (success, remaining_gas)

def vulnerable_contract_logic(initial_gas, function_to_test):


    success_ec, remaining_gas_ec = function_to_test(initial_gas)

    if remaining_gas_ec > 10:
        success_final_calc = True
        remaining_gas_final_calc = remaining_gas_ec - 10
    else:
        success_final_calc = False
        remaining_gas_final_calc = remaining_gas_ec

    return (success_ec, remaining_gas_ec, success_final_calc, remaining_gas_final_calc)

if __name__ == '__main__':

    print("Vulnerable ecRecover Example")
    initial_gas_ec = 2000
    success_ec, remaining_gas_ec, success_final_calc, remaining_gas_final_calc = vulnerable_contract_logic(initial_gas_ec, ecRecover_vulnerable)
    print(f"Initial Gas: {initial_gas_ec}, Success ecRecover: {success_ec}, Remaining gas ecRecover: {remaining_gas_ec}, Success Final Calc: {success_final_calc}, Remaining gas Final Calc {remaining_gas_final_calc}")

    initial_gas_ec = 4000
    success_ec, remaining_gas_ec, success_final_calc, remaining_gas_final_calc = vulnerable_contract_logic(initial_gas_ec, ecRecover_vulnerable)
    print(f"Initial Gas: {initial_gas_ec}, Success ecRecover: {success_ec}, Remaining gas ecRecover: {remaining_gas_ec}, Success Final Calc: {success_final_calc}, Remaining gas Final Calc {remaining_gas_final_calc}")

    print("\nVulnerable Identity Example")
    initial_gas_identity = 10
    success_identity, remaining_gas_identity, success_final_calc, remaining_gas_final_calc = vulnerable_contract_logic(initial_gas_identity, identity_vulnerable)
    print(f"Initial Gas: {initial_gas_identity}, Success identity: {success_identity}, Remaining gas identity: {remaining_gas_identity}, Success Final Calc: {success_final_calc}, Remaining gas Final Calc {remaining_gas_final_calc}")

    initial_gas_identity = 20
    success_identity, remaining_gas_identity, success_final_calc, remaining_gas_final_calc = vulnerable_contract_logic(initial_gas_identity, identity_vulnerable)
    print(f"Initial Gas: {initial_gas_identity}, Success identity: {success_identity}, Remaining gas identity: {remaining_gas_identity}, Success Final Calc: {success_final_calc}, Remaining gas Final Calc {remaining_gas_final_calc}")