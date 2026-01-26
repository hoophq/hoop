(ns webapp.components.forms
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex IconButton Select Text Tooltip]]
   ["lucide-react" :refer [Eye EyeOff HelpCircle Upload X]]
   [clojure.string :as cs]
   [reagent.core :as r]))

(defn- form-label
  [text]
  [:> Text {:size "1" :as "label" :weight "bold" :class "text-gray-12"}
   text])

(defn- form-label-dark
  [text]
  [:> Text {:size "1" :as "label" :weight "bold" :class "text-gray-12"}
   text])

(defn- form-helper-text
  [text]
  [:> Tooltip {:content text}
   [:> HelpCircle {:size 14}]])

(defn input
  "Multi purpose HTML input component.
  Props signature:
  :label -> html label text;
  :placeholder -> html prop placeholder for input;
  :value -> a reagent atom piece of state;
  :start-adornment -> component to render as left adornment (e.g., select dropdown)."
  [_]
  (let [eye-open? (r/atom true)
        toggle-eye #(swap! eye-open? not)]
    (fn [{:keys [label
                 placeholder
                 name
                 dark
                 id
                 helper-text
                 value
                 defaultValue
                 on-change
                 on-blur
                 on-keyDown
                 required
                 full-width?
                 pattern
                 disabled
                 minlength
                 maxlength
                 type
                 min
                 max
                 step
                 size
                 not-margin-bottom? ;; TODO: Remove this prop when remove margin-bottom from all inputs
                 hidden
                 start-adornment]}]
      [:div {:class (str "text-sm"
                         (when-not not-margin-bottom? " mb-regular")
                         (when full-width? " w-full")
                         (when hidden " hidden"))}
       (when (or label (not (cs/blank? helper-text)))
         [:div {:class "flex items-center mb-1 gap-2"}
          (when label
            (if dark
              [form-label-dark label]
              [form-label label]))
          (when (not (cs/blank? helper-text))
            [form-helper-text helper-text dark])])

       [:div {:class (str "rt-TextFieldRoot rt-variant-surface "
                          (case size
                            "3" "rt-r-size-3 "
                            "2" "rt-r-size-2 "
                            "1" "rt-r-size-1 "
                            "rt-r-size-3 ")
                          (when (= type "datetime-local") "*:block")
                          (when dark "dark"))}
        (when start-adornment
          [:div {:data-side "left" :class "rt-TextFieldSlot"}
           start-adornment])
        [:input
         {:type (if (= type "password")
                  (if @eye-open? "password" "text")
                  (or type "text"))
          :class "rt-reset rt-TextFieldInput"
          :id id
          :placeholder (or placeholder label)
          :name name
          :pattern pattern
          :minLength minlength
          :maxLength maxlength
          :min min
          :max max
          :step step
          :value value
          :defaultValue defaultValue
          :on-blur on-blur
          :on-change on-change
          :on-keyDown on-keyDown
          :disabled (or disabled false)
          :required (or required false)}]

        (when (= type "password")
          [:div {:data-side "right" :class "rt-TextFieldSlot"}
           [:button {:data-accent-color ""
                     :type "button"
                     :class (str "rt-reset rt-BaseButton rt-r-size-2 rt-variant-ghost rt-IconButton "
                                 (case size
                                   "3" "rt-r-size-2"
                                   "2" "rt-r-size-1"
                                   "rt-r-size-2"))
                     :on-click toggle-eye}
            (if @eye-open?
              [:> Eye {:size 16}]
              [:> EyeOff {:size 16}])]])]])))

(defn textarea
  [{:keys [label
           placeholder
           name
           dark
           size
           id
           helper-text
           value
           defaultValue
           on-change
           on-keyDown
           required
           on-blur
           rows
           autoFocus
           not-margin-bottom? ;; TODO: Remove this prop when remove margin-bottom from all inputs
           disabled]}]
  [:div {:class (when-not not-margin-bottom? "mb-regular")}
   [:div {:class "flex items-center gap-2 mb-1"}
    (if dark
      [form-label-dark label]
      [form-label label])
    (when (not (cs/blank? helper-text))
      [form-helper-text helper-text dark])]
   [:div {:class (str "rt-TextAreaRoot rt-variant-surface "
                      (case size
                        "3" "rt-r-size-3"
                        "2" "rt-r-size-2"
                        "1" "rt-r-size-1"
                        "rt-r-size-3"))}
    [:textarea
     {:class (str "rt-reset rt-TextAreaInput "
                  (when dark "dark"))
      :id (or id "")
      :rows (or rows 5)
      :name (or name "")
      :value value
      :defaultValue defaultValue
      :autoFocus autoFocus
      :placeholder placeholder
      :on-change on-change
      :on-blur on-blur
      :on-keyDown on-keyDown
      :disabled (or disabled false)
      :required (or required false)}]]])

(defn- option
  [item _]
  ^{:key (:value item)}
  [:> Select.Item {:value (:value item)} (:text item)])

(defn select
  "HTML select.
  Props signature:
  label -> html label text;
  options -> List of {:text string :value string};
  active -> the option value of an already active item;
  on-change -> function to be executed on change;
  required -> HTML required attribute;"
  [{:keys [label
           not-margin-bottom?
           variant
           helper-text
           name
           size
           options
           placeholder
           selected
           default-value
           on-change
           required
           disabled
           full-width?
           dark]}]
  [:div {:class (str " text-sm w-full"
                     (when-not not-margin-bottom? " mb-regular"))}
   [:div {:class "flex items-center gap-2 mb-1"}
    (if dark
      [form-label-dark label]
      [form-label label])
    (when (not (cs/blank? helper-text))
      [form-helper-text helper-text dark])]
   [:> Select.Root (merge
                    (when-not (cs/blank? default-value)
                      {:default-value default-value})
                    (when-not (cs/blank? selected)
                      {:value selected})
                    {:size (or size "3")
                     :name name
                     :on-value-change on-change
                     :required (or required false)
                     :disabled (or disabled false)})
    [:> Select.Trigger {:placeholder (or placeholder "Select one")
                        :variant (or variant "surface")
                        :color "indigo"
                        :class (str (when full-width? "w-full ")
                                    (when dark "dark"))}]
    [:> Select.Content {:position "popper"
                        :color "indigo"}
     (map #(option % selected) options)]]])


(defn file
  "File input component with base64 encoding support"
  [_]
  (let [file-input-ref (r/atom nil)
        file-name (r/atom nil)]
    (fn [{:keys [label on-change value helper-text required]}]
      (let [handle-file-change (fn [event]
                                 (when-let [file (-> event .-target .-files (aget 0))]
                                   (reset! file-name (.-name file))
                                   (let [reader (js/FileReader.)]
                                     (set! (.-onload reader)
                                           (fn [e]
                                             (when-let [base64 (second (cs/split (-> e .-target .-result) #"," 2))]
                                               (on-change base64))))
                                     (.readAsDataURL reader file))))
            handle-button-click (fn []
                                  (when @file-input-ref
                                    (.click @file-input-ref)))
            handle-clear (fn []
                           (reset! file-name nil)
                           (when @file-input-ref
                             (set! (.-value @file-input-ref) ""))
                           (on-change ""))]
        [:> Box {:class "text-sm mb-regular"}
         [:> Flex {:class "items-center gap-2 mb-1"}
          (when label
            [:> Text {:size "1" :as "label" :weight "bold" :class "text-gray-12"}
             label])
          (when (not (cs/blank? helper-text))
            [:> Tooltip {:content helper-text}
             [:> HelpCircle {:size 14}]])]
         [:> Flex {:class "items-center gap-2"}
          [:input
           {:type "file"
            :ref (fn [el] (reset! file-input-ref el))
            :on-change handle-file-change
            :required (and
                       (not= required "false")
                       (or required (nil? required)))
            :style {:display "none"}}]
          [:> Button
           {:variant "soft"
            :size "2"
            :type "button"
            :on-click handle-button-click}
           [:> Flex {:align "center" :gap "2"}
            [:> Upload {:size 16}]
            "Upload File"]]
          (when (not (cs/blank? value))
            [:> Flex {:align "center" :gap "2"}
             [:> Text {:size "1" :class "text-gray-11"}
              (or @file-name "File uploaded")]
             [:> IconButton
              {:variant "ghost"
               :color "gray"
               :size "1"
               :type "button"
               :on-click handle-clear}
              [:> X {:size 14}]]])]]))))
