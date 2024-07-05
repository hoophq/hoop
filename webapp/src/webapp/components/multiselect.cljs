(ns webapp.components.multiselect
  (:require [reagent.core :as r]
            ["react-select" :default Select]
            ["react-select/creatable" :default CreatableSelect]))

(def styles
  #js {"multiValue" (fn [style]
                      (clj->js (into (js->clj style)
                                     {"padding" "4px"
                                      "borderRadius" "6px"
                                      "backgroundColor" "#D1D5DB"})))
       "option" (fn [style]
                  (clj->js (into (js->clj style)
                                 {"fontSize" "0.75rem"})))
       "menuPortal" (fn [style]
                      (clj->js (into (js->clj style)
                                     {"z-index" "60"})))
       "control" (fn [style]
                   (clj->js (into (js->clj style)
                                  {"borderRadius" "6px"})))
       "input" (fn [style]
                 (clj->js (into (js->clj style)
                                {"& > input"
                                 #js {":focus"
                                      #js {"--tw-ring-shadow" "none"}}})))})

(defn- form-label
  [text]
  [:label {:class "mb-1 block text-xs font-semibold text-gray-800"} text])

(defn main [{:keys [default-value disabled? required? on-change options label id name]}]
  [:div {:class "mb-regular text-sm"}
   [:div {:class "flex items-center gap-2"}
    (when label
      [form-label label])]
   [:> Select
    {:value default-value
     :id id
     :name name
     :isMulti true
     :isDisabled disabled?
     :required required?
     :onChange on-change
     :options options
     :isClearable false
     :menuPortalTarget (.-body js/document)
     :className "react-select-container"
     :classNamePrefix "react-select"
     :styles styles}]])

(defn creatable-select [{:keys [default-value disabled? required? on-change options label id name]}]
  [:div {:class "mb-regular text-sm"}
   [:div {:class "flex items-center gap-2"}
    (when label
      [form-label label])]
   [:> CreatableSelect
    {:value default-value
     :id id
     :name name
     :isMulti true
     :isDisabled disabled?
     :required required?
     :onChange on-change
     :options options
     :isClearable false
     :menuPortalTarget (.-body js/document)
     :className "react-select-container"
     :classNamePrefix "react-select"
     :styles styles}]])

(defn text-input
  "Renders a text input that supports multiple values.

    Params:
    - value: A reagent atom that holds the current value of the text input.
    - input-value: A reagent atom that holds the current input value of the text input.
    - disabled?: A boolean indicating whether the text input is disabled.
    - required?: A boolean indicating whether the text input is required.
    - on-change: A function that is called when the value of the text input changes.
    - on-input-change: A function that is called when the input value of the text input changes.
    - label: A string that is used as the label of the text input.
    - id: A string that is used as the id of the text input.
    - name: A string that is used as the name of the text input.

    Behaviors:
    - When the 'Enter' or 'Tab' key is pressed, if there is a current input value, it is added to the value list and the input value is cleared.
    - The 'Enter' and 'Tab' key events are prevented from propagating to avoid unwanted side effects.
    - The text input is rendered using the CreatableSelect component from the react-select library.
    - The text input supports multiple values (isMulti is set to true).
    - The dropdown indicator of the text input is hidden (:components #js{:DropdownIndicator nil}).
    - The menu of the text input is always closed (:menuIsOpen false).
    - The text input can be cleared (:isClearable true).
    - The text input has specific class names for styling purposes (:className and :classNamePrefix)."

  [{:keys [value input-value disabled? required? on-change on-input-change label label-description id name]}]
  (let [handleKeyDown (fn [event]
                        (if @input-value
                          (case (.-key event)
                            "Enter" (do
                                      (reset! value (conj @value {"label" @input-value "value" @input-value}))
                                      (reset! input-value "")
                                      (.preventDefault event))
                            "Tab" (do
                                    (reset! value (conj @value {"label" @input-value "value" @input-value}))
                                    (reset! input-value "")
                                    (.preventDefault event))
                            nil)
                          nil))]
    [:div {:class "mb-regular text-sm"}
     [:div {:class "flex flex-col justify-center mb-1"}
      (when label
        [form-label label])
      (when label-description
        [:span {:class "text-xs text-gray-500"} label-description])]
     [:> CreatableSelect
      {:components #js{:DropdownIndicator nil}
       :value @value
       :inputValue @input-value
       :id id
       :name name
       :isMulti true
       :menuIsOpen false
       :isDisabled disabled?
       :required required?
       :onChange on-change
       :onInputChange on-input-change
       :onKeyDown handleKeyDown
       :isClearable true
       :className "react-select-container"
       :classNamePrefix "react-select"
       :styles styles}]]))